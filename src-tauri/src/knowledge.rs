// The knowledge references the RAG indexer is allowed to read.
//
// Until now the only way to feed the indexer was to set ORCHESTRA_KNOWLEDGE_DIR
// before launching the app: there was no button anywhere, so the Knowledge
// screen sat empty for anyone who did not already know that. This module is the
// missing piece — the list of references, persisted, plus the mounts and config
// the indexer container needs to see them.
//
// It registers references only. Which group a reference belongs to, and which
// scope that group serves, are declared on the Knowledge screen — that is where
// the hierarchy is authored, and duplicating a second, shallower notion of
// scope here would give two places to answer the same question.
//
// It lives in the Tauri shell rather than in a Go service because mounting is a
// host action. A local folder is bind-mounted read-only into the container, and
// a bind mount cannot be added to a container that is already running, so adding
// or removing one necessarily restarts the indexer. Putting the list anywhere
// else would mean the component that owns the container has to ask another
// process what to mount before that process has started.
//
// Folders are mounted READ-ONLY, and only into the indexer — never into a
// sandbox. An agent reaches this content solely through the gateway's /rag
// route, so registering a folder grants retrieval, not file access.

use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::sync::Mutex;

/// One registered reference: a local directory, or a document fetched over
/// HTTPS. These are the two kinds the indexer ingests.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Reference {
    /// "local" | "external"
    pub kind: String,
    /// local: the host directory. external: the https URL.
    pub path: String,
}

impl Reference {
    fn is_local(&self) -> bool {
        self.kind == "local"
    }

    /// The stable, unique name this reference's files are addressed under.
    ///
    /// The leaf alone is not enough: `~/a/docs` and `~/b/docs` are two
    /// references with one name, and their files would then share an
    /// identifier — which for the screen that assigns sources to groups means
    /// granting one grants the other. The suffix is a digest of the registered
    /// path, so it is unique per reference and, unlike a position in the list,
    /// does not change when the list is reordered. An identifier that moved
    /// would silently detach every group assignment made against it.
    fn id(&self) -> String {
        let digest = crate::providers::sha256_hex(self.path.as_bytes());
        format!("{}-{}", sanitize_segment(&self.label()), &digest[..8])
    }

    /// The leaf name shown as the source in the Knowledge screen.
    fn label(&self) -> String {
        if self.is_local() {
            Path::new(&self.path)
                .file_name()
                .map(|s| s.to_string_lossy().to_string())
                .unwrap_or_else(|| self.path.clone())
        } else {
            self.path.clone()
        }
    }
}

/// Reduces a name to something safe to use as a path segment. The identifier
/// becomes part of every source path the indexer stores, so a slash or a space
/// in a folder name must not turn one segment into two.
fn sanitize_segment(name: &str) -> String {
    let out: String = name
        .chars()
        .map(|c| if c.is_alphanumeric() || c == '-' || c == '_' || c == '.' { c } else { '-' })
        .collect();
    let trimmed = out.trim_matches('-').to_string();
    if trimmed.is_empty() { "source".into() } else { trimmed }
}

/// Where the registered references are recorded, and where the generated
/// indexer config is written. Under the app support dir, which is also a path
/// Docker Desktop will bind-mount on macOS.
fn state_dir() -> Option<PathBuf> {
    let home = std::env::var("HOME").ok().filter(|h| !h.is_empty())?;
    let dir = PathBuf::from(home).join("Library/Application Support/orchestra");
    std::fs::create_dir_all(&dir).ok()?;
    Some(dir)
}

fn list_path() -> Option<PathBuf> {
    state_dir().map(|d| d.join("knowledge-sources.json"))
}

/// The generated indexer config. The bundled configs/rag.json is a read-only
/// app resource, so the source list is written here rather than edited in place.
fn generated_config_path() -> Option<PathBuf> {
    state_dir().map(|d| d.join("rag.generated.json"))
}

fn caption_path() -> Option<PathBuf> {
    state_dir().map(|d| d.join("knowledge-caption.json"))
}

/// Whether images are described by a vision model so their contents become
/// searchable, and which model does it.
///
/// Off is the default and has to stay the default: a caption costs a model call
/// per picture, and registering a folder of screenshots should not quietly
/// start spending. This is where that decision is recorded, next to the
/// reference list, because it is the same kind of decision — what the indexer is
/// asked to do with the files it has been given.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CaptionSetting {
    /// Empty means off. The indexer reads it the same way.
    #[serde(default)]
    pub model: String,
    /// Gateway route the vision model sits behind. Empty follows the embedding
    /// route, which is what one provider serving both looks like.
    #[serde(default)]
    pub prefix: String,
}

impl Default for CaptionSetting {
    fn default() -> Self {
        Self { model: String::new(), prefix: String::new() }
    }
}

/// The stored caption setting, or the default (off) when never written or
/// unreadable — broken state must not turn spending on.
pub fn caption_setting() -> CaptionSetting {
    caption_path()
        .and_then(|p| std::fs::read_to_string(p).ok())
        .and_then(|raw| serde_json::from_str(&raw).ok())
        .unwrap_or_default()
}

fn write_caption_setting(c: &CaptionSetting) -> Result<(), String> {
    let path = caption_path().ok_or("no state directory")?;
    let raw = serde_json::to_string_pretty(c).map_err(|e| e.to_string())?;
    std::fs::write(path, raw).map_err(|e| e.to_string())
}

/// The registered references. An unreadable or corrupt file is treated as no
/// file rather than failing startup — broken state must not stop the app.
///
/// Back-compat: ORCHESTRA_KNOWLEDGE_DIR was the only way in before this
/// existed, so an install that used it keeps working and its folder simply
/// appears in the list. It is adopted only while the list has never been
/// written. Once the user has touched the list at all, the file is the whole
/// truth — including when they have emptied it. Falling back on an empty list
/// instead would make removing that folder impossible: it would reappear on the
/// next read, and no amount of clicking ✕ would get rid of it.
pub fn list() -> Vec<Reference> {
    if let Some(refs) = stored() {
        return refs;
    }
    match std::env::var("ORCHESTRA_KNOWLEDGE_DIR") {
        Ok(dir) if !dir.is_empty() && Path::new(&dir).is_dir() => {
            vec![Reference { kind: "local".into(), path: dir }]
        }
        _ => Vec::new(),
    }
}

/// The persisted list, or None when it has never been written.
fn stored() -> Option<Vec<Reference>> {
    let raw = std::fs::read_to_string(list_path()?).ok()?;
    serde_json::from_str(&raw).ok()
}

fn save(refs: &[Reference]) -> Result<(), String> {
    let path = list_path().ok_or("書き込み可能なアプリ領域が見つかりません")?;
    let raw = serde_json::to_string_pretty(refs).map_err(|e| e.to_string())?;
    std::fs::write(path, raw).map_err(|e| e.to_string())
}

/// Registers a reference. Idempotent.
///
/// A local path is checked for being a directory: a typo would otherwise be
/// accepted and then show up as a registered source that silently indexes
/// nothing, which is a much harder thing to notice than an error here.
pub fn add(kind: &str, path: &str) -> Result<Vec<Reference>, String> {
    let path = path.trim();
    if path.is_empty() {
        return Err("参照先を指定してください".into());
    }
    match kind {
        "local" => {
            if !Path::new(path).is_dir() {
                return Err(format!("{path} はフォルダではありません"));
            }
        }
        "external" => {
            if !path.starts_with("https://") {
                return Err("外部参照は https:// で指定してください".into());
            }
        }
        _ => return Err(format!("不明な種別です: {kind}")),
    }

    let mut refs = list();
    if !refs.iter().any(|r| r.path == path) {
        refs.push(Reference { kind: kind.to_string(), path: path.to_string() });
        save(&refs)?;
    }
    Ok(refs)
}

pub fn remove(path: &str) -> Result<Vec<Reference>, String> {
    let mut refs = list();
    refs.retain(|r| r.path != path);
    save(&refs)?;
    Ok(refs)
}

/// Validates one reference the same way `add` does, without registering it.
///
/// Split out so a bulk replace can check every row before writing any of them.
/// A half-applied list is worse than a rejected one: the caller asked for a
/// state, not for as much of it as happened to parse.
pub fn check(kind: &str, path: &str) -> Result<(), String> {
    match kind {
        "local" if !Path::new(path).is_dir() => Err(format!("{path} はフォルダではありません")),
        "external" if !path.starts_with("https://") => {
            Err("外部参照は https:// で指定してください".into())
        }
        "local" | "external" => Ok(()),
        _ => Err(format!("不明な種別です: {kind}")),
    }
}

/// Replaces the whole list in one write.
///
/// This is what a bulk sync is: the submitted list becomes the registered list,
/// including the removals. Doing it as a sequence of add/remove calls would
/// restart the indexer once per row — each restart costing a rebuild — and
/// would leave the list half-changed if one row failed partway through.
///
/// Every row is validated first and nothing is written unless all of them pass,
/// for the same reason `add` refuses a path that is not a folder: a reference
/// that indexes nothing is far harder to notice later than an error now.
pub fn replace(refs: Vec<Reference>) -> Result<Vec<Reference>, String> {
    let mut seen = std::collections::HashSet::new();
    let mut clean: Vec<Reference> = Vec::with_capacity(refs.len());
    for r in refs {
        let path = r.path.trim().to_string();
        if path.is_empty() {
            return Err("参照先が空の行があります".into());
        }
        check(&r.kind, &path)?;
        // A duplicate is dropped rather than refused: the same folder listed
        // twice means the same thing as listed once, and failing the whole
        // import over it would be pedantry.
        if seen.insert(path.clone()) {
            clean.push(Reference { kind: r.kind, path });
        }
    }
    save(&clean)?;
    Ok(clean)
}

/// The container path a local folder is mounted at.
///
/// The index prefix disambiguates two folders that share a leaf name — without
/// it, `~/a/docs` and `~/b/docs` would mount over each other and one would
/// silently disappear from the index.
fn container_path(reference: &Reference, index: usize) -> String {
    let safe: String = reference
        .label()
        .chars()
        .map(|c| if c.is_alphanumeric() || c == '-' || c == '_' || c == '.' { c } else { '-' })
        .collect();
    format!("/knowledge/{index}-{safe}")
}

/// `-v <host>:<container>:ro` for every registered local folder. External
/// references need no mount — the indexer fetches them over the network.
pub fn mount_args() -> Vec<String> {
    list()
        .iter()
        .enumerate()
        .filter(|(_, r)| r.is_local())
        .map(|(i, r)| format!("{}:{}:ro", r.path, container_path(r, i)))
        .collect()
}

/// Writes the indexer config — the bundled one with `sources` replaced by the
/// registered references — and returns its path.
///
/// One source per reference rather than one root covering them all, because the
/// Knowledge screen assigns retrieval permission per source; collapsing every
/// folder into a single "/knowledge" entry would leave nothing to assign.
///
/// Returns None when nothing is registered, so the caller falls back to the
/// bundled config unchanged.
pub fn write_generated_config(base: Option<&Path>) -> Option<PathBuf> {
    let refs = list();
    if refs.is_empty() {
        return None;
    }
    let mut cfg: serde_json::Value = base
        .and_then(|p| std::fs::read_to_string(p).ok())
        .and_then(|raw| serde_json::from_str(&raw).ok())
        .unwrap_or_else(|| serde_json::json!({}));
    if !cfg.is_object() {
        cfg = serde_json::json!({});
    }

    let sources: Vec<serde_json::Value> = refs
        .iter()
        .enumerate()
        .map(|(i, r)| {
            if r.is_local() {
                serde_json::json!({
                    "kind": "local",
                    "root": container_path(r, i),
                    "name": r.label(),
                    // What the indexer addresses this reference's files under.
                    // Stable across reordering, unique across references — see
                    // Reference::id.
                    "id": r.id(),
                })
            } else {
                serde_json::json!({
                    "kind": "external",
                    "url": r.path,
                    "name": r.label(),
                    "id": r.id(),
                })
            }
        })
        .collect();
    cfg["sources"] = serde_json::Value::Array(sources);

    // Captioning travels with the source list because it is read at the same
    // moment: the indexer decides how to reduce a picture while ingesting it.
    let caption = caption_setting();
    cfg["captionModel"] = serde_json::Value::String(caption.model.clone());
    cfg["captionPrefix"] = serde_json::Value::String(caption.prefix.clone());

    let path = generated_config_path()?;
    std::fs::write(&path, serde_json::to_string_pretty(&cfg).ok()?).ok()?;
    Some(path)
}

/// The resolved config directory, kept so a change to the reference list can
/// restart the indexer with the configuration the app booted with.
pub struct ConfigDir(pub Mutex<Option<PathBuf>>);

#[tauri::command]
pub fn knowledge_sources() -> Vec<Reference> {
    list()
}

#[tauri::command]
pub fn knowledge_source_add(
    kind: String,
    path: String,
    state: tauri::State<ConfigDir>,
) -> Result<Vec<Reference>, String> {
    let refs = add(&kind, &path)?;
    restart_indexer(&state);
    Ok(refs)
}

#[tauri::command]
pub fn knowledge_source_remove(
    path: String,
    state: tauri::State<ConfigDir>,
) -> Result<Vec<Reference>, String> {
    let refs = remove(&path)?;
    restart_indexer(&state);
    Ok(refs)
}

/// Replaces the registered references wholesale and restarts the indexer once.
#[tauri::command]
pub fn knowledge_sources_replace(
    refs: Vec<Reference>,
    state: tauri::State<ConfigDir>,
) -> Result<Vec<Reference>, String> {
    let saved = replace(refs)?;
    restart_indexer(&state);
    Ok(saved)
}

#[tauri::command]
pub fn knowledge_caption() -> CaptionSetting {
    caption_setting()
}

/// Turns captioning on or off and restarts the indexer so the next build reads
/// the new setting. Turning it OFF costs nothing to apply; turning it on means
/// the next build describes every picture it has not seen before, which is why
/// the UI says so before calling this.
#[tauri::command]
pub fn knowledge_caption_set(
    model: String,
    prefix: String,
    state: tauri::State<ConfigDir>,
) -> Result<CaptionSetting, String> {
    let next = CaptionSetting { model: model.trim().to_string(), prefix: prefix.trim().to_string() };
    write_caption_setting(&next)?;
    restart_indexer(&state);
    Ok(next)
}

/// A bind mount cannot be added to a running container, so a change to the
/// reference list only takes effect on a fresh one. The indexer keeps nothing
/// durable — its store is in memory and rebuilt from the sources — so a restart
/// costs a re-index and nothing else.
fn restart_indexer(state: &tauri::State<ConfigDir>) {
    let dir = state.0.lock().ok().and_then(|d| d.clone());
    crate::sidecars::start_rag_container(&dir);
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::MutexGuard;

    // These tests redirect HOME so they operate on a throwaway state dir. That
    // is process-wide, so they must not run concurrently.
    static ENV_LOCK: Mutex<()> = Mutex::new(());

    struct Env {
        _guard: MutexGuard<'static, ()>,
        _dir: tempish::Dir,
    }

    fn with_temp_home() -> Env {
        let guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        let dir = tempish::Dir::new();
        std::env::set_var("HOME", dir.path());
        std::env::remove_var("ORCHESTRA_KNOWLEDGE_DIR");
        Env { _guard: guard, _dir: dir }
    }

    /// A minimal temp directory that removes itself, so the tests need no
    /// dev-dependency for something this small.
    mod tempish {
        use std::path::{Path, PathBuf};
        pub struct Dir(PathBuf);
        impl Dir {
            pub fn new() -> Self {
                let base = std::env::temp_dir().join(format!(
                    "orchestra-knowledge-test-{}-{:?}",
                    std::process::id(),
                    std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_nanos()
                ));
                std::fs::create_dir_all(&base).unwrap();
                Dir(base)
            }
            pub fn path(&self) -> &Path {
                &self.0
            }
            pub fn subdir(&self, name: &str) -> PathBuf {
                let p = self.0.join(name);
                std::fs::create_dir_all(&p).unwrap();
                p
            }
        }
        impl Drop for Dir {
            fn drop(&mut self) {
                let _ = std::fs::remove_dir_all(&self.0);
            }
        }
    }

    #[test]
    fn registers_and_removes_a_local_folder() {
        let env = with_temp_home();
        let docs = env._dir.subdir("docs");
        let docs = docs.to_string_lossy().to_string();

        let refs = add("local", &docs).expect("add");
        assert_eq!(refs.len(), 1);
        assert_eq!(list().len(), 1, "the list must survive being re-read from disk");

        // Adding the same folder twice is a no-op rather than a duplicate mount.
        assert_eq!(add("local", &docs).unwrap().len(), 1);

        assert!(remove(&docs).unwrap().is_empty());
        assert!(list().is_empty());
    }

    // The leaf alone would give ~/a/docs and ~/b/docs one name, and their files
    // would then share an identifier — granting one would grant the other.
    #[test]
    fn ids_are_unique_across_folders_sharing_a_leaf_name() {
        let a = Reference { kind: "local".into(), path: "/Users/me/a/docs".into() };
        let b = Reference { kind: "local".into(), path: "/Users/me/b/docs".into() };
        assert_eq!(a.label(), b.label(), "precondition: the leaves collide");
        assert_ne!(a.id(), b.id(), "ids must not");
        assert!(a.id().starts_with("docs-"), "the id stays readable: {}", a.id());
    }

    // The id is stored inside every group assignment made against this
    // reference, so it must not move when the list is edited around it.
    #[test]
    fn ids_are_stable() {
        let r = Reference { kind: "local".into(), path: "/Users/me/docs".into() };
        assert_eq!(r.id(), Reference { kind: "local".into(), path: "/Users/me/docs".into() }.id());
    }

    // The id becomes a path segment in the indexer, so a name carrying a slash
    // or a space must not turn one segment into two.
    #[test]
    fn ids_are_safe_as_a_path_segment() {
        let r = Reference { kind: "local".into(), path: "/Users/me/my docs & notes".into() };
        assert!(!r.id().contains('/') && !r.id().contains(' '), "{}", r.id());
    }

    // A bulk sync is the submitted list, removals included.
    #[test]
    fn replace_swaps_the_whole_list() {
        let env = with_temp_home();
        let keep = env._dir.subdir("keep").to_string_lossy().to_string();
        let gone = env._dir.subdir("gone").to_string_lossy().to_string();
        let fresh = env._dir.subdir("fresh").to_string_lossy().to_string();
        add("local", &keep).unwrap();
        add("local", &gone).unwrap();

        let saved = replace(vec![
            Reference { kind: "local".into(), path: keep.clone() },
            Reference { kind: "local".into(), path: fresh.clone() },
            Reference { kind: "external".into(), path: "https://example.com/a.md".into() },
        ])
        .expect("replace");

        assert_eq!(saved.len(), 3);
        let paths: Vec<_> = list().into_iter().map(|r| r.path).collect();
        assert!(paths.contains(&keep) && paths.contains(&fresh));
        assert!(!paths.contains(&gone), "a sync must drop what the list omits");
    }

    // Nothing is written unless every row passes. A half-applied list is worse
    // than a rejected one: the caller asked for a state, not for as much of it
    // as happened to parse.
    #[test]
    fn replace_is_all_or_nothing() {
        let env = with_temp_home();
        let good = env._dir.subdir("good").to_string_lossy().to_string();
        let missing = env._dir.path().join("nope").to_string_lossy().to_string();
        add("local", &good).unwrap();

        let err = replace(vec![
            Reference { kind: "local".into(), path: good.clone() },
            Reference { kind: "local".into(), path: missing },
        ])
        .expect_err("a row naming a non-folder must fail the import");
        assert!(err.contains("フォルダではありません"), "{err}");

        let paths: Vec<_> = list().into_iter().map(|r| r.path).collect();
        assert_eq!(paths, vec![good], "a rejected import must leave the list untouched");
    }

    // An empty list is a legitimate answer — it is what clearing everything
    // looks like — and must not be confused with "nothing was submitted".
    #[test]
    fn replace_accepts_an_empty_list() {
        let env = with_temp_home();
        let docs = env._dir.subdir("docs").to_string_lossy().to_string();
        add("local", &docs).unwrap();
        assert!(replace(vec![]).unwrap().is_empty());
        assert!(list().is_empty());
    }

    // A path that is not a folder would be accepted and then show up as a
    // registered source that silently indexes nothing — much harder to notice
    // than an error at the point of adding it.
    #[test]
    fn refuses_a_path_that_is_not_a_folder() {
        let env = with_temp_home();
        let missing = env._dir.path().join("nope").to_string_lossy().to_string();
        assert!(add("local", &missing).is_err());
        assert!(add("local", "  ").is_err());
        assert!(add("external", "http://insecure.example").is_err(), "external must be https");
        assert!(add("rubbish", "https://x.example").is_err());
        assert!(list().is_empty());
    }

    // Two folders can share a leaf name. Without disambiguation they would
    // mount over each other and one would vanish from the index without a word.
    #[test]
    fn folders_sharing_a_leaf_name_get_distinct_mounts() {
        let env = with_temp_home();
        let a = env._dir.subdir("a");
        std::fs::create_dir_all(a.join("docs")).unwrap();
        let b = env._dir.subdir("b");
        std::fs::create_dir_all(b.join("docs")).unwrap();
        let a_docs = a.join("docs").to_string_lossy().to_string();
        let b_docs = b.join("docs").to_string_lossy().to_string();

        add("local", &a_docs).unwrap();
        add("local", &b_docs).unwrap();

        let mounts = mount_args();
        assert_eq!(mounts.len(), 2);
        let targets: Vec<&str> = mounts.iter().map(|m| m.split(':').nth(1).unwrap()).collect();
        assert_ne!(targets[0], targets[1], "mounts collided: {mounts:?}");
        for m in &mounts {
            assert!(m.ends_with(":ro"), "knowledge must be mounted read-only: {m}");
        }
    }

    // An external document is fetched over the network, so it must not produce
    // a bind mount — and it must still appear as its own source.
    #[test]
    fn external_references_are_indexed_without_a_mount() {
        let _env = with_temp_home();
        add("external", "https://example.com/handbook.md").unwrap();

        assert!(mount_args().is_empty());

        let path = write_generated_config(None).expect("config written");
        let raw = std::fs::read_to_string(path).unwrap();
        let cfg: serde_json::Value = serde_json::from_str(&raw).unwrap();
        let sources = cfg["sources"].as_array().unwrap();
        assert_eq!(sources.len(), 1);
        assert_eq!(sources[0]["kind"], "external");
        assert_eq!(sources[0]["url"], "https://example.com/handbook.md");
    }

    // One source per reference, because the Knowledge screen assigns retrieval
    // permission per source; a single collapsed root would leave nothing to
    // assign. The base config's other settings must survive.
    #[test]
    fn generated_config_keeps_the_base_settings() {
        let env = with_temp_home();
        let base = env._dir.path().join("rag.json");
        std::fs::write(&base, r#"{"listen":"0.0.0.0:8790","embedModel":"text-embedding-3-small"}"#).unwrap();
        let docs = env._dir.subdir("handbook").to_string_lossy().to_string();
        add("local", &docs).unwrap();

        let path = write_generated_config(Some(&base)).expect("config written");
        let cfg: serde_json::Value = serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap();

        assert_eq!(cfg["embedModel"], "text-embedding-3-small", "base settings were dropped");
        let sources = cfg["sources"].as_array().unwrap();
        assert_eq!(sources.len(), 1);
        assert_eq!(sources[0]["kind"], "local");
        assert_eq!(sources[0]["name"], "handbook");
        assert!(sources[0]["root"].as_str().unwrap().starts_with("/knowledge/"));
    }

    // With nothing registered the caller must fall back to the bundled config
    // rather than start the indexer with an empty source list.
    #[test]
    fn no_references_means_no_generated_config() {
        let _env = with_temp_home();
        assert!(write_generated_config(None).is_none());
        assert!(mount_args().is_empty());
    }

    // The env var was the only way in before this existed; an install that used
    // it keeps working, and removing the folder in the UI has to stick rather
    // than being re-adopted on the next read.
    #[test]
    fn adopts_the_legacy_env_var_only_while_nothing_is_registered() {
        let env = with_temp_home();
        let legacy = env._dir.subdir("legacy").to_string_lossy().to_string();
        std::env::set_var("ORCHESTRA_KNOWLEDGE_DIR", &legacy);

        assert_eq!(list().len(), 1, "legacy folder should be adopted");
        assert_eq!(list()[0].path, legacy);

        // Registering anything writes the list, which materialises the adopted
        // folder rather than dropping something the user was relying on.
        let other = env._dir.subdir("other").to_string_lossy().to_string();
        add("local", &other).unwrap();
        let paths: Vec<String> = list().into_iter().map(|r| r.path).collect();
        assert_eq!(paths, vec![legacy.clone(), other.clone()]);

        // And once written, the file is the truth — emptying it sticks, even
        // with the env var still set. Otherwise the folder would reappear on
        // the next read and could never be removed.
        remove(&legacy).unwrap();
        remove(&other).unwrap();
        assert!(list().is_empty(), "an emptied list must not re-adopt the env var");

        std::env::remove_var("ORCHESTRA_KNOWLEDGE_DIR");
    }
}
