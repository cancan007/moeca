// Host-side GitHub App credentials for the Delivery issue pull.
//
// The App ID + private key are kept in the OS keychain (never in the webview /
// localStorage) and pushed over loopback to the host agent, which mints
// per-repo installation tokens and calls api.github.com directly. The gateway
// is not involved — the issue pull is host-trusted code, not a sandboxed agent.

use keyring::Entry;
use serde_json::{json, Value};

const KC_SERVICE: &str = "orchestra-github-app";

fn entry(name: &str) -> Result<Entry, String> {
    Entry::new(KC_SERVICE, name).map_err(|e| e.to_string())
}
fn kc_set(name: &str, val: &str) -> Result<(), String> {
    let e = entry(name)?;
    if val.is_empty() {
        let _ = e.delete_credential();
        return Ok(());
    }
    e.set_password(val).map_err(|e| e.to_string())
}
fn kc_get(name: &str) -> Option<String> {
    entry(name).ok()?.get_password().ok()
}
fn kc_del(name: &str) {
    if let Ok(e) = entry(name) {
        let _ = e.delete_credential();
    }
}

fn hostagent_base() -> String {
    std::env::var("ORCHESTRA_HOSTAGENT_URL").unwrap_or_else(|_| "http://127.0.0.1:8788".into())
}

// push sends the credentials to the host agent, which validates the key (parses
// the PEM) and holds it in memory. An error means the host agent rejected it.
fn push(app_id: &str, private_key: &str) -> Result<(), String> {
    ureq::post(&format!("{}/github/app", hostagent_base()))
        .send_json(json!({ "appId": app_id, "privateKey": private_key }))
        .map(|_| ())
        .map_err(|e| match e {
            ureq::Error::Status(_, resp) => resp
                .into_json::<Value>()
                .ok()
                .and_then(|v| v.get("error").and_then(|s| s.as_str()).map(str::to_string))
                .unwrap_or_else(|| "host agent rejected the credentials".into()),
            other => format!("host agent /github/app: {other}"),
        })
}

/// Store the GitHub App credentials (keychain) and push them to the host agent.
/// The host agent validates the private key before we persist it.
#[tauri::command]
pub fn github_app_set(app_id: String, private_key: String) -> Result<Value, String> {
    let app_id = app_id.trim().to_string();
    if app_id.is_empty() || private_key.trim().is_empty() {
        return Err("App ID と秘密鍵は必須です".into());
    }
    push(&app_id, &private_key)?; // validate before persisting
    kc_set("app-id", &app_id)?;
    kc_set("private-key", &private_key)?;
    Ok(json!({ "configured": true, "appId": app_id }))
}

/// Whether a GitHub App is configured (App ID only; the key is never returned).
#[tauri::command]
pub fn github_app_status() -> Value {
    let id = kc_get("app-id");
    json!({
        "configured": id.is_some() && kc_get("private-key").is_some(),
        "appId": id.unwrap_or_default(),
    })
}

/// Remove the GitHub App credentials (keychain + host agent).
#[tauri::command]
pub fn github_app_clear() -> Result<Value, String> {
    kc_del("app-id");
    kc_del("private-key");
    let _ = ureq::delete(&format!("{}/github/app", hostagent_base())).call();
    Ok(json!({ "configured": false }))
}

/// Re-push the stored credentials to the host agent (called once the host agent
/// is up, since it holds them in memory only). No-op when none are stored.
#[tauri::command]
pub fn github_app_resync() -> Value {
    if let (Some(id), Some(key)) = (kc_get("app-id"), kc_get("private-key")) {
        let ok = push(&id, &key).is_ok();
        return json!({ "configured": true, "appId": id, "pushed": ok });
    }
    json!({ "configured": false })
}
