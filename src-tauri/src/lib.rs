// Orchestra native shell.
// On launch it spawns the Go sidecars (security gateway, host agent, docker
// sandbox controller) and tears them down on exit. The webview UI talks to them
// over loopback HTTP.

mod githubapp;
mod knowledge;
mod providers;
mod sidecars;

use knowledge::ConfigDir;
use providers::AdminState;
use sidecars::Sidecars;
use std::path::PathBuf;
use std::sync::Mutex;
use tauri::{Manager, RunEvent};

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .setup(|app| {
            // Config dir: ORCHESTRA_CONFIG_DIR (dev) overrides the bundled
            // `configs/` resource shipped alongside the app.
            let config_dir = std::env::var("ORCHESTRA_CONFIG_DIR")
                .ok()
                .map(PathBuf::from)
                .or_else(|| app.path().resource_dir().ok().map(|d| d.join("configs")));
            // The admin token authorizes provider config/secret writes. The
            // gateway receives only its hash; the raw token stays in this
            // process (managed state) and never reaches the webview or a sandbox.
            let (admin_sha256, admin_token) = providers::generate_admin_token();
            let children = sidecars::spawn_all(config_dir.clone(), &admin_sha256, &admin_token);
            app.manage(Sidecars(Mutex::new(children)));
            // Kept so registering a knowledge folder can restart the indexer
            // with the same configuration the app booted with.
            app.manage(ConfigDir(Mutex::new(config_dir)));
            app.manage(AdminState::new(admin_token));
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            ping,
            sidecar_count,
            providers::provider_sync,
            providers::provider_set_secret,
            providers::provider_delete,
            providers::provider_list,
            providers::gateway_logs,
            providers::gateway_metrics,
            githubapp::github_app_set,
            githubapp::github_app_status,
            githubapp::github_app_clear,
            githubapp::github_app_resync,
            knowledge::knowledge_sources,
            knowledge::knowledge_source_add,
            knowledge::knowledge_source_remove,
            knowledge::knowledge_caption,
            knowledge::knowledge_caption_set
        ])
        .build(tauri::generate_context!())
        .expect("error while building Orchestra")
        .run(|app, event| {
            if let RunEvent::Exit = event {
                if let Some(state) = app.try_state::<Sidecars>() {
                    if let Ok(mut children) = state.0.lock() {
                        sidecars::kill_all(&mut children);
                    }
                }
            }
        });
}

#[tauri::command]
fn ping() -> String {
    "pong".into()
}

/// Number of sidecar processes currently managed.
#[tauri::command]
fn sidecar_count(state: tauri::State<Sidecars>) -> usize {
    state.0.lock().map(|c| c.len()).unwrap_or(0)
}
