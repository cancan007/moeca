import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { open } from "@tauri-apps/plugin-dialog";
import { sectionTitle } from "./ui";
import { isDesktop } from "@/lib/providers";
import { delivery, type RepoInfo } from "@/lib/delivery";
import { githubApp, type GitHubAppStatus } from "@/lib/githubApp";

// RepositoriesPanel manages the Delivery repositories explicitly from the UI.
// The host agent persists them in its store and reads them live, so an added
// repo's worktrees appear as Delivery cards immediately (no restart). Repos
// declared in the config file show as read-only seeds (managed=false).
export function RepositoriesPanel() {
  const { t } = useTranslation();
  const [repos, setRepos] = useState<RepoInfo[]>([]);
  const [online, setOnline] = useState<boolean | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const [target, setTarget] = useState("main");
  const [ci, setCi] = useState("");
  const [busy, setBusy] = useState(false);
  const desktop = isDesktop();

  const refresh = async () => {
    try {
      setRepos(await delivery.repos());
      setOnline(true);
      setErr(null);
    } catch (e) {
      setOnline(false);
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  const pick = async () => {
    try {
      const sel = await open({ directory: true, multiple: false, title: t("settings.repos.pickDialogTitle") });
      if (typeof sel === "string") {
        setPath(sel);
        if (!name.trim()) setName(sel.split("/").filter(Boolean).pop() ?? "");
      }
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  const add = async () => {
    setBusy(true);
    setErr(null);
    try {
      const ciCommand = ci.trim() ? ci.trim().split(/\s+/) : undefined;
      await delivery.addRepo({ name: name.trim(), path: path.trim(), target: target.trim() || "main", ciCommand });
      setName(""); setPath(""); setTarget("main"); setCi("");
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (r: RepoInfo) => {
    setBusy(true);
    setErr(null);
    try {
      await delivery.removeRepo(r.name);
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const canAdd = name.trim() !== "" && path.trim() !== "" && !busy;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
      {sectionTitle(t("settings.repos.title"), t("settings.repos.desc"))}

      <GitHubAppSection />

      {online === false && (
        <div style={{ font: "400 11px 'IBM Plex Sans'", color: "#d39a4e", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "10px 13px" }}>
          {t("errors.hostAgentOfflineDesktop")}
        </div>
      )}
      {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "9px 12px" }}>{err}</div>}

      {/* add */}
      <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "14px 16px", display: "flex", flexDirection: "column", gap: 10 }}>
        <div style={{ display: "flex", gap: 10 }}>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("settings.repos.namePlaceholder")} style={{ ...inp, flex: 1 }} />
          <input value={target} onChange={(e) => setTarget(e.target.value)} placeholder={t("settings.repos.targetPlaceholder")} style={{ ...inp, width: 220, fontFamily: "'IBM Plex Mono',monospace" }} />
        </div>
        <div style={{ display: "flex", gap: 10 }}>
          <input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/Users/you/path/to/repo" style={{ ...inp, flex: 1, fontFamily: "'IBM Plex Mono',monospace" }} />
          {desktop && (
            <div onClick={pick} title={t("settings.repos.pickFolder")} style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx2)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "8px 14px", borderRadius: 7, cursor: "pointer", whiteSpace: "nowrap" }}>
              {t("settings.repos.pickShort")}
            </div>
          )}
        </div>
        <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
          <input value={ci} onChange={(e) => setCi(e.target.value)} placeholder={t("settings.repos.ciPlaceholder")} style={{ ...inp, flex: 1, fontFamily: "'IBM Plex Mono',monospace" }} />
          <div onClick={() => canAdd && add()} style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 18px", borderRadius: 7, cursor: canAdd ? "pointer" : "default", opacity: canAdd ? 1 : 0.5, whiteSpace: "nowrap" }}>
            {busy ? t("settings.repos.saving") : t("common.add")}
          </div>
        </div>
      </div>

      {/* list */}
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {repos.length === 0 && (
          <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "4px 2px" }}>{t("settings.repos.none")}</div>
        )}
        {repos.map((r) => (
          <div key={r.name} style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 10, padding: "11px 14px", display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
            <div style={{ width: 7, height: 7, borderRadius: "50%", background: "#4f9dff", flex: "none" }} />
            <span style={{ font: "600 12px 'IBM Plex Mono'", color: "var(--tx2)" }}>{r.name}</span>
            <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "#5b9fe8", background: "var(--tint-blue)", border: "1px solid var(--bd2)", padding: "2px 7px", borderRadius: 5 }}>→ {r.target}</span>
            {r.githubSlug && (
              <span title={t("settings.repos.githubSlugTip")} style={{ font: "500 8.5px 'IBM Plex Mono'", color: "#67c9a4", background: "var(--tint-green)", border: "1px solid var(--tint-green-bd)", padding: "2px 7px", borderRadius: 5 }}>GH: {r.githubSlug}</span>
            )}
            {r.ciCommand && r.ciCommand.length > 0 && (
              <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx3)", background: "var(--bg-inset2)", border: "1px solid var(--bd3)", padding: "2px 7px", borderRadius: 5 }}>CI: {r.ciCommand.join(" ")}</span>
            )}
            {!r.managed && (
              <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 7px", borderRadius: 5 }}>config</span>
            )}
            <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", flexBasis: "100%" }}>{r.path}</span>
            <div style={{ flex: 1 }} />
            {r.managed ? (
              <div onClick={() => !busy && remove(r)} title={t("common.delete")} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--red)", cursor: busy ? "default" : "pointer", padding: "5px 10px", border: "1px solid var(--tint-red-bd)", borderRadius: 7, background: "var(--tint-red)" }}>
                {t("common.delete")}
              </div>
            ) : (
              <span style={{ font: "400 9.5px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "5px 4px" }}>{t("settings.repos.configManaged")}</span>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

const inp: React.CSSProperties = {
  background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7,
  padding: "8px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none",
  boxSizing: "border-box",
};

// GitHubAppSection — configure a host-side GitHub App for the Delivery issue
// pull. The App ID + private key are kept in the OS keychain (via Tauri) and
// held in memory by the host agent, which mints per-repo installation tokens and
// calls GitHub directly — no gateway, least privilege (Issues: Read).
function GitHubAppSection() {
  const { t } = useTranslation();
  const desktop = isDesktop();
  const [status, setStatus] = useState<GitHubAppStatus | null>(null);
  const [appId, setAppId] = useState("");
  const [key, setKey] = useState("");
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!desktop) { setStatus({ configured: false }); return; }
    githubApp.status().then(setStatus).catch(() => setStatus({ configured: false }));
  }, [desktop]);

  const save = async () => {
    setBusy(true); setErr(null);
    try {
      const s = await githubApp.set(appId.trim(), key);
      setStatus(s); setAppId(""); setKey(""); setEditing(false);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const clear = async () => {
    setBusy(true); setErr(null);
    try { setStatus(await githubApp.clear()); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  };

  const configured = status?.configured;
  const showForm = !configured || editing;

  return (
    <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "14px 16px", display: "flex", flexDirection: "column", gap: 11 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
        <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.repos.ghApp.title")}</span>
        <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: configured ? "#67c9a4" : "var(--tx-faint)", background: configured ? "var(--tint-green)" : "var(--bg-card2)", border: `1px solid ${configured ? "var(--tint-green-bd)" : "var(--bd2)"}`, padding: "2px 8px", borderRadius: 5 }}>
          {configured ? t("settings.repos.ghApp.configured", { appId: status?.appId }) : t("settings.repos.ghApp.notConfigured")}
        </span>
      </div>
      <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx3)", lineHeight: 1.55 }}>
        {t("settings.repos.ghApp.desc")}
      </span>
      {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</div>}

      {!desktop && <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("settings.repos.ghApp.desktopOnly")}</span>}

      {desktop && showForm && (
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          <input value={appId} onChange={(e) => setAppId(e.target.value)} placeholder={t("settings.repos.ghApp.appIdPlaceholder")} style={{ ...inp, fontFamily: "'IBM Plex Mono',monospace" }} />
          <textarea value={key} onChange={(e) => setKey(e.target.value)} spellCheck={false} placeholder={t("settings.repos.ghApp.keyPlaceholder")} style={{ ...inp, height: 108, resize: "vertical", fontFamily: "'IBM Plex Mono',monospace", fontSize: 10.5, lineHeight: 1.5 }} />
          <div style={{ display: "flex", gap: 8 }}>
            <div onClick={() => !busy && appId.trim() && key.trim() && save()} style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 16px", borderRadius: 7, cursor: busy ? "default" : "pointer", opacity: appId.trim() && key.trim() && !busy ? 1 : 0.5 }}>
              {busy ? t("settings.repos.ghApp.verifying") : t("common.save")}
            </div>
            {editing && <div onClick={() => { setEditing(false); setAppId(""); setKey(""); }} style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx3)", padding: "8px 14px", border: "1px solid var(--bd2)", borderRadius: 7, cursor: "pointer" }}>{t("common.cancel")}</div>}
          </div>
        </div>
      )}

      {desktop && configured && !editing && (
        <div style={{ display: "flex", gap: 8 }}>
          <div onClick={() => setEditing(true)} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx3)", padding: "6px 12px", border: "1px solid var(--bd2)", borderRadius: 7, cursor: "pointer" }}>{t("settings.repos.ghApp.update")}</div>
          <div onClick={() => !busy && clear()} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--red)", padding: "6px 12px", border: "1px solid var(--tint-red-bd)", borderRadius: 7, background: "var(--tint-red)", cursor: busy ? "default" : "pointer" }}>{t("common.delete")}</div>
        </div>
      )}
    </div>
  );
}
