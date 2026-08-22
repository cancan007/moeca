// Sidecar management: Orchestra's Go services run when the app launches and are
// torn down on exit.
//
// The host agent and sandbox controller run as loopback child processes. The
// security GATEWAY, by contrast, runs as a Docker container attached to the
// internal sandbox egress network — that is the only way to enforce that a
// strict sandbox can reach the gateway and NOTHING else (not the host, not the
// internet). The gateway is the sole holder of API keys and the sole component
// with internet access. Set ORCHESTRA_GATEWAY_MODE=host to fall back to running
// the gateway as a plain loopback binary (no network-layer isolation) for
// Docker-less development of the gateway/Audit UI.

use crate::knowledge;
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;

/// Managed sidecar child processes (Tauri state).
pub struct Sidecars(pub Mutex<Vec<Child>>);

struct Service {
    bin: &'static str,
    config: &'static str,
    /// service cannot run without a config; skip if none is provided.
    requires_config: bool,
}

/// Host-process sidecars (loopback). The gateway is handled separately (docker).
const SERVICES: &[Service] = &[
    Service { bin: "orchestra-hostagent", config: "hostagent", requires_config: false },
    Service { bin: "orchestra-sandbox", config: "sandbox", requires_config: false },
];

/// Docker names/images for the containerized gateway and its networks.
const GATEWAY_CONTAINER: &str = "orchestra-gateway";
const GATEWAY_IMAGE: &str = "orchestra/gateway:latest";
const EGRESS_NETWORK: &str = "orchestra-egress"; // --internal: no host/internet route
const UPSTREAM_NETWORK: &str = "orchestra-upstream"; // ordinary bridge for gateway egress

/// RAG indexer container: reachable by the gateway (for /rag) on the upstream
/// network — NOT on the sandbox egress network — plus a loopback-published
/// management port. Knowledge is a read-only mount held only here.
const RAG_CONTAINER: &str = "orchestra-rag";
const RAG_IMAGE: &str = "orchestra/rag:latest";

/// Package-registry proxy: the dependency-fetch counterpart to the gateway.
/// Dual-homed exactly like it (egress + upstream), because a strict sandbox has
/// no route to the internet and `npm install` / `pip install` / `go mod
/// download` would otherwise be impossible. It serves GET/HEAD only, to fixed
/// upstreams, so it cannot become a way to publish a package or reach an
/// arbitrary host.
const REGISTRY_CONTAINER: &str = "orchestra-registry";
const REGISTRY_IMAGE: &str = "orchestra/registry:latest";

/// Directory holding the built Go binaries. Override with ORCHESTRA_BIN_DIR;
/// defaults to the directory of the running executable.
fn bin_dir() -> PathBuf {
    if let Ok(d) = std::env::var("ORCHESTRA_BIN_DIR") {
        return PathBuf::from(d);
    }
    std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(|p| p.to_path_buf()))
        .unwrap_or_else(|| PathBuf::from("."))
}

/// Resolves `<config_dir>/<name>.json` if it exists, else None.
fn config_path(name: &str, config_dir: &Option<PathBuf>) -> Option<PathBuf> {
    let dir = config_dir.as_ref()?;
    let path = dir.join(format!("{name}.json"));
    if path.exists() {
        Some(path)
    } else {
        None
    }
}

/// Returns ["-config", "<path>"] when the config file exists, else None.
fn config_args(name: &str, config_dir: &Option<PathBuf>) -> Option<Vec<String>> {
    config_path(name, config_dir).map(|p| vec!["-config".into(), p.to_string_lossy().into_owned()])
}

/// True if `path` lives under a macOS Docker Desktop default shared root, so a
/// bind mount from it is permitted.
fn is_docker_shareable(path: &std::path::Path) -> bool {
    let s = path.to_string_lossy();
    ["/Users/", "/Volumes/", "/private/", "/tmp/", "/var/folders/"]
        .iter()
        .any(|root| s.starts_with(root))
}

/// Docker Desktop on macOS only bind-mounts files under a fixed set of shared
/// roots (/Users, /Volumes, /private, /tmp, /var/folders). The bundled configs
/// live under /Applications/<app>.app/Contents/Resources/configs, which is NOT
/// shared — so the gateway/rag container config mounts are denied and the app
/// falls back to mock mode. When the resolved config dir is outside a shareable
/// root, copy the *.json into the app-support dir (under $HOME, always shared)
/// and use that instead. A dev-provided ORCHESTRA_CONFIG_DIR under /Users is left
/// untouched, so live config edits still take effect.
fn ensure_shareable_config_dir(dir: Option<PathBuf>) -> Option<PathBuf> {
    let dir = dir?;
    if is_docker_shareable(&dir) {
        return Some(dir);
    }
    let home = match std::env::var("HOME") {
        Ok(h) if !h.is_empty() => h,
        _ => return Some(dir), // no HOME to stage into; mount may still fail
    };
    let staged = PathBuf::from(home).join("Library/Application Support/orchestra/configs");
    if let Err(e) = std::fs::create_dir_all(&staged) {
        eprintln!("[sidecar] could not create staged config dir {}: {e}", staged.display());
        return Some(dir);
    }
    if let Ok(entries) = std::fs::read_dir(&dir) {
        for entry in entries.flatten() {
            let p = entry.path();
            if p.extension().and_then(|e| e.to_str()) == Some("json") {
                if let Some(name) = p.file_name() {
                    let _ = std::fs::copy(&p, staged.join(name));
                }
            }
        }
    }
    eprintln!("[sidecar] staged configs to Docker-shareable dir {}", staged.display());
    Some(staged)
}

/// Spawn all available sidecars. Missing binaries/Docker are logged and skipped
/// rather than aborting app startup, so the UI still runs in mock mode.
/// `admin_sha256` is the hex SHA-256 of the admin token; the gateway gets only
/// the hash (the raw token stays in the Tauri process).
pub fn spawn_all(config_dir: Option<PathBuf>, admin_sha256: &str, admin_token: &str) -> Vec<Child> {
    let mut children = Vec::new();

    // Ensure the config dir is one Docker can bind-mount (the bundled
    // /Applications path is not shared) — otherwise containers fail and the UI
    // falls back to mock mode.
    let config_dir = ensure_shareable_config_dir(config_dir);

    // Gateway: container by default, host binary only when explicitly requested.
    let host_mode = std::env::var("ORCHESTRA_GATEWAY_MODE").as_deref() == Ok("host");
    if host_mode {
        if let Some(child) = spawn_gateway_binary(&config_dir, admin_sha256) {
            children.push(child);
        }
    } else {
        start_gateway_container(&config_dir, admin_sha256);
        start_rag_container(&config_dir);
        start_registry_container(&config_dir);
    }

    // Host-process sidecars.
    let dir = bin_dir();
    for svc in SERVICES {
        let path = dir.join(svc.bin);
        if !path.exists() {
            eprintln!("[sidecar] {} not found at {}", svc.bin, path.display());
            continue;
        }
        let cfg = config_args(svc.config, &config_dir);
        if svc.requires_config && cfg.is_none() {
            eprintln!("[sidecar] {} requires a config (set ORCHESTRA_CONFIG_DIR); skipping", svc.bin);
            continue;
        }
        let mut cmd = Command::new(&path);
        if let Some(args) = cfg {
            cmd.args(args);
        }
        // The child watches this pid and self-terminates if we die without
        // running the graceful reaper (e.g. SIGKILL/crash).
        cmd.env("ORCHESTRA_PARENT_PID", std::process::id().to_string());
        // A sidecar that shells out to docker (the sandbox controller) inherits
        // this process's PATH — launchd's bare /usr/bin:/bin:… when the app was
        // launched from Finder, which has no docker on it. Hand it both the
        // augmented PATH and the binary we already resolved, so it cannot end up
        // looking somewhere else than the shell does.
        cmd.env("PATH", augmented_path());
        cmd.env("ORCHESTRA_DOCKER_BIN", docker_bin());
        // The sandbox controller mints a gateway session per run so a run can
        // carry its own knowledge scope. That needs the RAW admin token, which
        // no sandbox ever receives — the controller is a host process, and it
        // is already the component that decides what a sandbox may reach.
        if svc.bin.ends_with("sandbox") {
            cmd.env("ORCHESTRA_ADMIN_TOKEN", admin_token);
        }
        cmd.stdout(Stdio::inherit()).stderr(Stdio::inherit());
        match cmd.spawn() {
            Ok(child) => {
                eprintln!("[sidecar] {} started (pid {})", svc.bin, child.id());
                children.push(child);
            }
            Err(e) => eprintln!("[sidecar] failed to start {}: {e}", svc.bin),
        }
    }
    children
}

/// Resolves an absolute path to the `docker` CLI. A GUI-launched app (Finder /
/// `open`) does NOT inherit the shell PATH — it gets only /usr/bin:/bin:/usr/sbin
/// :/sbin — so a Homebrew or Docker-Desktop `docker` (typically /opt/homebrew/bin
/// or /usr/local/bin) is not found and every container silently fails to start,
/// degrading the UI to mock mode. Honor ORCHESTRA_DOCKER_BIN, then probe the
/// usual install locations, then fall back to bare "docker" (PATH) for launches
/// that do inherit a PATH (terminal / dev).
fn docker_bin() -> String {
    if let Ok(p) = std::env::var("ORCHESTRA_DOCKER_BIN") {
        if !p.is_empty() {
            return p;
        }
    }
    let home = std::env::var("HOME").unwrap_or_default();
    let candidates = [
        "/opt/homebrew/bin/docker".to_string(),
        "/usr/local/bin/docker".to_string(),
        "/Applications/Docker.app/Contents/Resources/bin/docker".to_string(),
        format!("{home}/.docker/bin/docker"),
        "/usr/bin/docker".to_string(),
    ];
    for c in candidates {
        if std::path::Path::new(&c).exists() {
            return c;
        }
    }
    "docker".to_string()
}

/// A `docker` Command with the resolved absolute binary and a PATH augmented with
/// the common CLI locations, so the docker CLI can also locate its own helper
/// binaries (credential helpers, buildx) under a GUI launch that lacks a PATH.
/// This process's PATH with the usual docker install locations prepended. Used
/// for every docker invocation and handed to the Go sidecars, which shell out to
/// docker themselves.
fn augmented_path() -> String {
    let extra = "/opt/homebrew/bin:/usr/local/bin:/Applications/Docker.app/Contents/Resources/bin:/usr/bin:/bin";
    match std::env::var("PATH") {
        Ok(p) if !p.is_empty() => format!("{extra}:{p}"),
        _ => extra.to_string(),
    }
}

fn docker_cmd() -> Command {
    let mut cmd = Command::new(docker_bin());
    cmd.env("PATH", augmented_path());
    cmd
}

/// Runs a docker command, inheriting env (so `--env NAME` pass-through works).
/// Returns true on success. Never panics; a missing docker binary is reported.
fn docker(args: &[&str]) -> bool {
    match docker_cmd().args(args).stdout(Stdio::inherit()).stderr(Stdio::inherit()).status() {
        Ok(s) => s.success(),
        Err(e) => {
            eprintln!("[gateway] docker {args:?} failed to run: {e}");
            false
        }
    }
}

/// Where the RAG indexer keeps its embedded vectors between runs. Created here
/// rather than in the container so the mount has something to bind to, and kept
/// under the app support dir with the rest of the derived state.
fn vector_cache_dir() -> Option<PathBuf> {
    let home = std::env::var("HOME").ok().filter(|h| !h.is_empty())?;
    let dir = PathBuf::from(home).join("Library/Application Support/orchestra/rag-cache");
    if let Err(e) = std::fs::create_dir_all(&dir) {
        eprintln!("[rag] could not create the vector cache dir {}: {e}", dir.display());
        return None;
    }
    Some(dir)
}

/// Ensures a docker network exists (idempotent; errors ignored if it already does).
fn ensure_network(name: &str, internal: bool) {
    // `network inspect` succeeds only if it already exists.
    if docker_cmd().args(["network", "inspect", name]).stdout(Stdio::null()).stderr(Stdio::null()).status().map(|s| s.success()).unwrap_or(false) {
        return;
    }
    let mut args = vec!["network", "create"];
    if internal {
        args.push("--internal");
    }
    args.push(name);
    docker(&args);
}

/// Starts the gateway as a hardened container, dual-homed on both the upstream
/// bridge (its own internet egress) and the internal egress network (where
/// sandboxes reach it), holding the API keys and publishing only 127.0.0.1:8787
/// to the host UI.
///
/// Network order matters: the container's PRIMARY network is the non-internal
/// `orchestra-upstream`, so the `-p 127.0.0.1:8787` publish actually binds — a
/// container whose primary network is `--internal` does NOT get its ports
/// published (Docker records the binding but never activates it). The internal
/// `orchestra-egress` NIC is then added with `network connect`. This does not
/// weaken isolation: sandboxes stay egress-only (`--internal`, no route to
/// host/internet), and the gateway's L7 SSRF-deny still governs what it will
/// proxy — neither depends on the gateway's primary network.
fn start_gateway_container(config_dir: &Option<PathBuf>, admin_sha256: &str) {
    let cfg = match config_path("gateway", config_dir) {
        Some(p) => p,
        None => {
            eprintln!("[gateway] no gateway.json (set ORCHESTRA_CONFIG_DIR); skipping — UI runs in mock mode");
            return;
        }
    };
    let cfg = cfg.to_string_lossy().into_owned();

    ensure_network(EGRESS_NETWORK, true);
    ensure_network(UPSTREAM_NETWORK, false);
    // Remove any stale container from a previous (possibly crashed) run.
    docker(&["rm", "-f", GATEWAY_CONTAINER]);

    let mount = format!("{cfg}:/config/gateway.json:ro");
    let publish = "127.0.0.1:8787:8787";
    // Durable, tamper-evident audit plane: persist the hash-chained access log
    // to a host volume so it survives container restarts (in-memory ring alone
    // is capped at 500 and lost on restart). Override the host path with
    // ORCHESTRA_AUDIT_DIR; defaults under the app support dir.
    let audit_host = std::env::var("ORCHESTRA_AUDIT_DIR").unwrap_or_else(|_| {
        let home = std::env::var("HOME").unwrap_or_else(|_| "/tmp".into());
        format!("{home}/Library/Application Support/orchestra/gateway-audit")
    });
    let _ = std::fs::create_dir_all(&audit_host);
    let audit_mount = format!("{audit_host}:/audit");
    // Only the admin token HASH reaches the gateway (raw token stays host-side).
    // Provider API keys are NOT passed via --env: they are pushed over the
    // loopback admin API (from the keychain / bring-your-own env), so they never
    // appear in `docker inspect`.
    let admin_env = format!("ORCHESTRA_ADMIN_TOKEN_SHA256={admin_sha256}");
    let ok = docker(&[
        "run", "-d",
        "--name", GATEWAY_CONTAINER,
        "--label", "orchestra=1",
        // Primary = non-internal upstream so the published port binds (see fn doc).
        "--network", UPSTREAM_NETWORK,
        "-p", publish,
        "--env", &admin_env,
        "--env", "ORCHESTRA_AUDIT_DIR=/audit",
        "-v", &mount,
        "-v", &audit_mount,
        GATEWAY_IMAGE,
        "-config", "/config/gateway.json",
    ]);
    if !ok {
        eprintln!("[gateway] container failed to start (build it: `docker build -t {GATEWAY_IMAGE} gateway/`)");
        return;
    }
    // Add the internal egress NIC so strict sandboxes can reach the gateway
    // (at orchestra-gateway:8787) — while the sandboxes themselves have no route
    // off that island.
    docker(&["network", "connect", EGRESS_NETWORK, GATEWAY_CONTAINER]);
    eprintln!("[gateway] container {GATEWAY_CONTAINER} started on {UPSTREAM_NETWORK}+{EGRESS_NETWORK} (published {publish})");
}

/// Starts (or restarts) the RAG indexer container on the upstream network
/// (gateway-reachable, not sandbox-reachable). The registered knowledge folders
/// are mounted read-only under /knowledge; the session token lets it call the
/// gateway for embeddings (it holds no API key).
///
/// This is also the restart path used when the user adds or removes a folder in
/// Settings, which is why it removes any existing container first: a bind mount
/// cannot be added to a running container, so the folder set only ever takes
/// effect on a fresh one. The indexer keeps nothing durable — its store is in
/// memory and rebuilt from its sources — so restarting costs a re-index and
/// nothing else.
pub fn start_rag_container(config_dir: &Option<PathBuf>) {
    ensure_network(UPSTREAM_NETWORK, false);
    docker(&["rm", "-f", RAG_CONTAINER]);

    let mut args: Vec<String> = vec![
        "run".into(), "-d".into(),
        "--name".into(), RAG_CONTAINER.into(),
        "--label".into(), "orchestra=1".into(),
        "--network".into(), UPSTREAM_NETWORK.into(),
        "-p".into(), "127.0.0.1:8790:8790".into(),
        "--env".into(), "ORCHESTRA_GATEWAY=http://orchestra-gateway:8787".into(),
        "--env".into(), "ORCHESTRA_SESSION=dev-session-token".into(),
    ];

    // Forwarded so the app can be looked at without an embeddings provider
    // configured: ORCHESTRA_EMBED_MODE=offline builds the index from locally
    // computed vectors. It is opt-in from the environment, never a fallback —
    // an index that silently stopped using the model would be worse than one
    // that stayed empty and said why.
    if let Ok(mode) = std::env::var("ORCHESTRA_EMBED_MODE") {
        if !mode.is_empty() {
            args.push("--env".into());
            args.push(format!("ORCHESTRA_EMBED_MODE={mode}"));
        }
    }

    // A writable volume for the embedded vectors, so a restart does not pay the
    // provider again for text that has not changed. It is the only writable
    // path this container has: knowledge stays read-only, and what is kept here
    // is derived data the indexer can always rebuild without it.
    if let Some(dir) = vector_cache_dir() {
        args.push("--env".into());
        args.push("ORCHESTRA_RAG_CACHE=/cache".into());
        args.push("-v".into());
        args.push(format!("{}:/cache", dir.to_string_lossy()));
    }

    // Read-only knowledge mounts — the only thing here that touches host-local
    // files, and the reason this function is the restart path.
    let mounts = knowledge::mount_args();
    for m in &mounts {
        args.push("-v".into());
        args.push(m.clone());
    }

    // The bundled rag.json is an app resource and read-only, so when folders are
    // registered the source list is written to a generated copy instead. With
    // nothing registered, the bundled config is used unchanged and the legacy
    // single-root behaviour still applies.
    let bundled = config_path("rag", config_dir);
    let cfg = knowledge::write_generated_config(bundled.as_deref()).or(bundled);
    if let Some(cfg) = cfg {
        args.push("-v".into());
        args.push(format!("{}:/config/rag.json:ro", cfg.to_string_lossy()));
        args.push(RAG_IMAGE.into());
        args.push("-config".into());
        args.push("/config/rag.json".into());
    } else {
        args.push(RAG_IMAGE.into());
    }

    let refs: Vec<&str> = args.iter().map(String::as_str).collect();
    if docker(&refs) {
        eprintln!("[rag] container {RAG_CONTAINER} started on {UPSTREAM_NETWORK} ({} knowledge mount(s), published 127.0.0.1:8790)", mounts.len());
    } else {
        eprintln!("[rag] container failed to start (build it: `docker build -t {RAG_IMAGE} ragindex/`)");
    }
}

/// Starts the package-registry proxy, dual-homed on the upstream bridge (its own
/// route out) and the internal egress network (where sandboxes reach it at
/// `orchestra-registry:8791`).
///
/// This is the same multi-homing trick as the gateway, applied to a second kind
/// of traffic, and for the same reason: a strict sandbox has no route off the
/// egress island, so the only way `npm install` can work is for the destination
/// to be a container ON that island which then makes its own upstream request.
/// The network order matters for the same reason too — the primary network must
/// be the non-internal `orchestra-upstream` or the published loopback port never
/// activates.
///
/// The artifact cache is a host volume rather than something shared into
/// sandboxes: the proxy owns it and sandboxes can only read it over HTTP. A
/// cache mount shared between tasks would let one task plant bytes that another
/// task later executes.
fn start_registry_container(config_dir: &Option<PathBuf>) {
    ensure_network(EGRESS_NETWORK, true);
    ensure_network(UPSTREAM_NETWORK, false);
    docker(&["rm", "-f", REGISTRY_CONTAINER]);

    let cache_host = std::env::var("ORCHESTRA_REGISTRY_CACHE_DIR").unwrap_or_else(|_| {
        let home = std::env::var("HOME").unwrap_or_else(|_| "/tmp".into());
        format!("{home}/Library/Application Support/orchestra/registry-cache")
    });
    let _ = std::fs::create_dir_all(&cache_host);
    let cache_mount = format!("{cache_host}:/cache");

    let mut args: Vec<String> = vec![
        "run".into(), "-d".into(),
        "--name".into(), REGISTRY_CONTAINER.into(),
        "--label".into(), "orchestra=1".into(),
        // Primary = non-internal upstream so the published port binds; the
        // internal egress NIC is added below with `network connect`.
        "--network".into(), UPSTREAM_NETWORK.into(),
        "-p".into(), "127.0.0.1:8791:8791".into(),
        "--env".into(), "ORCHESTRA_REGISTRY_CACHE_DIR=/cache".into(),
        "-v".into(), cache_mount,
    ];
    if let Some(cfg) = config_path("registry", config_dir) {
        args.push("-v".into());
        args.push(format!("{}:/config/registry.json:ro", cfg.to_string_lossy()));
        args.push(REGISTRY_IMAGE.into());
        args.push("-config".into());
        args.push("/config/registry.json".into());
    } else {
        args.push(REGISTRY_IMAGE.into());
    }

    let refs: Vec<&str> = args.iter().map(String::as_str).collect();
    if !docker(&refs) {
        eprintln!("[registry] container failed to start (build it: `docker build -t {REGISTRY_IMAGE} registry/`) — dependency installs inside sandboxes will fail");
        return;
    }
    docker(&["network", "connect", EGRESS_NETWORK, REGISTRY_CONTAINER]);
    eprintln!("[registry] container {REGISTRY_CONTAINER} started on {UPSTREAM_NETWORK}+{EGRESS_NETWORK} (cache {cache_host})");
}

/// Fallback: run the gateway as a loopback binary (ORCHESTRA_GATEWAY_MODE=host).
/// No network-layer isolation — for Docker-less gateway/Audit development only.
fn spawn_gateway_binary(config_dir: &Option<PathBuf>, admin_sha256: &str) -> Option<Child> {
    let path = bin_dir().join("orchestra-gateway");
    if !path.exists() {
        eprintln!("[sidecar] orchestra-gateway not found at {}", path.display());
        return None;
    }
    let cfg = config_args("gateway", config_dir)?;
    let mut cmd = Command::new(&path);
    cmd.args(cfg);
    cmd.env("ORCHESTRA_PARENT_PID", std::process::id().to_string());
    cmd.env("ORCHESTRA_ADMIN_TOKEN_SHA256", admin_sha256);
    // Host mode is a bare process on the host: bind loopback only so the gateway
    // is never exposed on the machine's external interfaces (unlike the
    // container, which binds 0.0.0.0 but publishes only 127.0.0.1 to the host).
    cmd.env("ORCHESTRA_LISTEN", "127.0.0.1:8787");
    cmd.stdout(Stdio::inherit()).stderr(Stdio::inherit());
    match cmd.spawn() {
        Ok(child) => {
            eprintln!("[sidecar] orchestra-gateway started in host mode (pid {})", child.id());
            Some(child)
        }
        Err(e) => {
            eprintln!("[sidecar] failed to start orchestra-gateway: {e}");
            None
        }
    }
}

/// Terminate all managed sidecars (best effort) on app exit. Kills the host
/// child processes and removes the gateway container (a no-op in host mode).
pub fn kill_all(children: &mut Vec<Child>) {
    for mut child in children.drain(..) {
        let _ = child.kill();
        let _ = child.wait();
    }
    for name in [GATEWAY_CONTAINER, RAG_CONTAINER, REGISTRY_CONTAINER] {
        let _ = docker_cmd()
            .args(["rm", "-f", name])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status();
    }
}
