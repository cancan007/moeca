// Provider (LLM connection) management, host side.
//
// Security model:
// - The RAW admin token lives ONLY here (Tauri process memory). The gateway is
//   given only its SHA-256, so a leak of gateway memory / `docker inspect`
//   reveals a useless hash.
// - Provider API keys are stored at rest in the OS KEYCHAIN and pushed to the
//   gateway over loopback (in-memory there). They never touch localStorage, the
//   webview, `docker --env`, or a sandbox.
// - The webview never sees the admin token: it calls these Tauri commands, and
//   only Rust talks to the gateway admin API.

use std::collections::HashMap;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use tauri::State;

const KEYCHAIN_SERVICE: &str = "orchestra-gateway-provider";

/// Managed state: the raw admin token (host-only) and the gateway base URL.
pub struct AdminState {
    pub token: Mutex<String>,
    pub base: String,
}

impl AdminState {
    pub fn new(token: String) -> Self {
        AdminState { token: Mutex::new(token), base: "http://127.0.0.1:8787".into() }
    }
    fn token(&self) -> Result<String, String> {
        let t = self.token.lock().map_err(|e| e.to_string())?;
        if t.is_empty() {
            return Err("admin token not initialized".into());
        }
        Ok(t.clone())
    }
}

/// Generate a random admin token and its hex SHA-256. The raw token stays in the
/// app; the gateway is configured with the hash only.
pub fn generate_admin_token() -> (String, String) {
    let raw: [u8; 32] = rand::random();
    let token = hex::encode(raw);
    (sha256_hex(token.as_bytes()), token)
}

pub fn sha256_hex(b: &[u8]) -> String {
    let mut h = Sha256::new();
    h.update(b);
    hex::encode(h.finalize())
}

/// Non-secret provider fields as sent from the webview (camelCase JSON).
#[derive(Serialize, Deserialize, Clone)]
pub struct ProviderInput {
    pub name: String,
    #[serde(default)]
    pub kind: String,
    pub prefix: String,
    #[serde(default)]
    pub upstream: String,
    #[serde(default)]
    pub allowlist: Vec<String>,
    #[serde(default)]
    pub models: Vec<String>,
    #[serde(default, rename = "injectHeaders")]
    pub inject_headers: HashMap<String, String>,
    /// Per-session token budget (0 => unlimited). `None` (omitted) leaves the
    /// gateway's existing budget untouched, so a UI edit never wipes a limit.
    #[serde(default, rename = "maxTokensPerSession")]
    pub max_tokens_per_session: Option<i64>,
}

/* ── OS keychain (at rest) ── */

fn kc_set(name: &str, value: &str) -> Result<(), String> {
    let entry = keyring::Entry::new(KEYCHAIN_SERVICE, name).map_err(|e| e.to_string())?;
    if value.is_empty() {
        let _ = entry.delete_credential();
        return Ok(());
    }
    entry.set_password(value).map_err(|e| e.to_string())
}

fn kc_get(name: &str) -> Option<String> {
    keyring::Entry::new(KEYCHAIN_SERVICE, name).ok()?.get_password().ok()
}

fn kc_delete(name: &str) {
    if let Ok(e) = keyring::Entry::new(KEYCHAIN_SERVICE, name) {
        let _ = e.delete_credential();
    }
}

/// Bring-your-own bootstrap: a provider key supplied via the host environment
/// (not persisted). Used only when the keychain has no key for that provider.
fn env_seed(name: &str) -> Option<String> {
    let var = match name {
        "anthropic" => "ANTHROPIC_API_KEY",
        "openai" => "OPENAI_API_KEY",
        "gemini" => "GEMINI_API_KEY",
        "github" => "GITHUB_TOKEN",
        _ => return None,
    };
    std::env::var(var).ok().filter(|v| !v.is_empty())
}

/* ── gateway admin API (loopback, admin token) ── */

fn gw_put_provider(st: &AdminState, p: &ProviderInput) -> Result<(), String> {
    let tok = st.token()?;
    let mut body = json!({
        "name": p.name, "kind": p.kind, "prefix": p.prefix, "upstream": p.upstream,
        "allowlist": p.allowlist, "models": p.models, "injectHeaders": p.inject_headers,
    });
    // Only send the budget when the UI supplied one, so an omit preserves the
    // gateway's current value (see the admin handler's merge semantics).
    if let Some(max) = p.max_tokens_per_session {
        body["maxTokensPerSession"] = json!(max);
    }
    ureq::put(&format!("{}/_gateway/providers", st.base))
        .set("X-Orchestra-Admin", &tok)
        .send_json(body)
        .map(|_| ())
        .map_err(|e| format!("gateway upsert {}: {e}", p.name))
}

fn gw_put_secret(st: &AdminState, name: &str, value: &str) -> Result<(), String> {
    let tok = st.token()?;
    ureq::put(&format!("{}/_gateway/providers/secret", st.base))
        .set("X-Orchestra-Admin", &tok)
        .send_json(json!({ "name": name, "value": value }))
        .map(|_| ())
        .map_err(|e| format!("gateway set secret {name}: {e}"))
}

fn gw_delete(st: &AdminState, name: &str) -> Result<(), String> {
    let tok = st.token()?;
    ureq::delete(&format!("{}/_gateway/providers?name={}", st.base, urlencode(name)))
        .set("X-Orchestra-Admin", &tok)
        .call()
        .map(|_| ())
        .map_err(|e| format!("gateway delete {name}: {e}"))
}

/// Returns the gateway's live, secret-free provider list (the UI's source of truth).
fn gw_list(st: &AdminState) -> Result<Value, String> {
    let tok = st.token()?;
    ureq::get(&format!("{}/_gateway/providers", st.base))
        .set("X-Orchestra-Admin", &tok)
        .call()
        .map_err(|e| format!("gateway list: {e}"))?
        .into_json::<Value>()
        .map_err(|e| e.to_string())
}

/// Wait until the gateway health endpoint responds (bounded), so a sync issued
/// right after launch doesn't race the container coming up.
fn wait_ready(st: &AdminState) {
    for _ in 0..60 {
        if ureq::get(&format!("{}/_gateway/health", st.base)).call().is_ok() {
            return;
        }
        std::thread::sleep(std::time::Duration::from_millis(250));
    }
}

/* ── Tauri commands (called by the webview; admin token stays in Rust) ── */

/// Apply the full provider set to the gateway: upsert each provider's non-secret
/// config, then (re)inject its secret from the keychain — or, if absent, from a
/// bring-your-own environment variable. Returns the live provider list.
#[tauri::command]
pub fn provider_sync(state: State<AdminState>, providers: Vec<ProviderInput>) -> Result<Value, String> {
    wait_ready(&state);
    for p in &providers {
        gw_put_provider(&state, p)?;
        let secret = kc_get(&p.name).or_else(|| env_seed(&p.name));
        if let Some(s) = secret {
            if !s.is_empty() {
                gw_put_secret(&state, &p.name, &s)?;
            }
        }
    }
    gw_list(&state)
}

/// Store a provider's API key at rest (keychain) and push it to the gateway.
/// The value is write-only: it is never read back to the webview.
#[tauri::command]
pub fn provider_set_secret(state: State<AdminState>, name: String, value: String) -> Result<Value, String> {
    kc_set(&name, &value)?;
    gw_put_secret(&state, &name, &value)?;
    gw_list(&state)
}

/// Remove a provider from the gateway and delete its key from the keychain.
#[tauri::command]
pub fn provider_delete(state: State<AdminState>, name: String) -> Result<Value, String> {
    gw_delete(&state, &name)?;
    kc_delete(&name);
    gw_list(&state)
}

#[tauri::command]
pub fn provider_list(state: State<AdminState>) -> Result<Value, String> {
    gw_list(&state)
}

/// Admin-gated gateway access logs (the monitoring plane). Includes captured
/// request/response content + run/stage attribution. Only the host (this
/// process, holding the admin token) can read it — never a sandbox.
#[tauri::command]
pub fn gateway_logs(state: State<AdminState>) -> Result<Value, String> {
    gw_admin_get(&state, "/_gateway/logs")
}

#[tauri::command]
pub fn gateway_metrics(state: State<AdminState>) -> Result<Value, String> {
    gw_admin_get(&state, "/_gateway/metrics")
}

fn gw_admin_get(st: &AdminState, path: &str) -> Result<Value, String> {
    let tok = st.token()?;
    ureq::get(&format!("{}{}", st.base, path))
        .set("X-Orchestra-Admin", &tok)
        .call()
        .map_err(|e| format!("gateway {path}: {e}"))?
        .into_json::<Value>()
        .map_err(|e| e.to_string())
}

fn urlencode(s: &str) -> String {
    s.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => (b as char).to_string(),
            _ => format!("%{:02X}", b),
        })
        .collect()
}
