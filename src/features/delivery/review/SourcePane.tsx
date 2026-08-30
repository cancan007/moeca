import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { DeliveryTask } from "@/store/useStore";
import { useStore } from "@/store/useStore";
import { hostagent } from "@/lib/hostagent";

export function SourcePane({ task, onOpenWorkspace }: { task: DeliveryTask; onOpenWorkspace: () => void }) {
  const { t } = useTranslation();
  const live = !!task.live;
  const refreshLive = useStore((s) => s.refreshLive);
  const repo = task.project;
  const branch = task.branch;

  const [files, setFiles] = useState<string[]>([]);
  const [path, setPath] = useState<string>("");
  const [orig, setOrig] = useState<string>("");
  const [src, setSrc] = useState<string>("");
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const dirty = src !== orig;

  // Live: load the changed files, then the first file's content.
  useEffect(() => {
    if (!live) return;
    let cancelled = false;
    (async () => {
      try {
        const diff = await hostagent.diff(repo, branch);
        const paths = diff.map((f) => f.path);
        if (cancelled) return;
        setFiles(paths);
        if (paths.length) void loadFile(paths[0]);
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [live, repo, branch]);

  const loadFile = async (p: string) => {
    setBusy("load"); setErr(null); setSaved(false);
    try {
      const content = await hostagent.file(repo, branch, p);
      setPath(p); setOrig(content); setSrc(content);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const save = async () => {
    if (!live) return;
    setBusy("save"); setErr(null);
    try {
      await hostagent.writeFile(repo, branch, path, src);
      setOrig(src); setSaved(true);
      await refreshLive(); // edit resets the CI gate
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const label = live ? (path || "(no changed files)") : `${task.worktree} / indexer.ts`;

  return (
    <div style={{ display: "flex", flexDirection: "column" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 9, padding: "11px 16px", borderBottom: "1px solid var(--bd)" }}>
        <svg width="12" height="12" viewBox="0 0 14 14" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5"><path d="M4 2v6a2 2 0 0 0 2 2h4M4 2a1.5 1.5 0 1 1 0 .01M10 10a1.5 1.5 0 1 1 0 .01M4 8V4" /></svg>
        {live && files.length > 0 ? (
          <select value={path} onChange={(e) => loadFile(e.target.value)} style={{ background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 6, color: "var(--tx2)", font: "500 10.5px 'IBM Plex Mono'", padding: "3px 7px", colorScheme: "dark", cursor: "pointer", maxWidth: 320 }}>
            {files.map((f) => <option key={f} value={f}>{f}</option>)}
          </select>
        ) : (
          <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx2)" }}>{label}</span>
        )}
        <span style={{ font: "500 9px 'IBM Plex Mono'", color: dirty ? "var(--amber)" : saved ? "#67c9a4" : "var(--tx-faint)", background: dirty ? "var(--tint-amber)" : "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 6px", borderRadius: 4 }}>{t(dirty ? "review.editing" : saved ? "review.savedState" : "review.untouched")}</span>
        <div style={{ flex: 1 }} />
        {dirty && <div onClick={() => setSrc(orig)} style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx3)", cursor: "pointer", padding: "4px 9px", border: "1px solid var(--bd2)", borderRadius: 6 }}>{t("review.revert")}</div>}
        <div onClick={onOpenWorkspace} style={{ display: "flex", alignItems: "center", gap: 6, font: "600 10.5px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer", padding: "4px 10px", border: "1px solid var(--tint-active-bd)", borderRadius: 6, background: "var(--tint-active)" }}>
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="var(--ac)" strokeWidth="1.5"><rect x="2" y="3" width="12" height="10" rx="1.5" /><path d="M2 6h12M5 3v10" /></svg>
          {t("review.openInWorkspace")}
        </div>
      </div>
      <textarea
        value={src}
        onChange={(e) => { setSrc(e.target.value); setSaved(false); }}
        spellCheck={false}
        readOnly={live && !path}
        style={{ height: 440, resize: "vertical", border: "none", outline: "none", background: "var(--bg-deep)", color: "var(--tx2)", fontFamily: "'IBM Plex Mono',monospace", fontSize: 12, lineHeight: 1.85, padding: "16px 18px", tabSize: 2 }}
      />
      <div style={{ padding: "11px 16px", borderTop: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 9, background: "var(--bg-panel)" }}>
        <span style={{ font: "400 10px 'IBM Plex Mono'", color: err ? "var(--red)" : "var(--tx-faint)" }}>{err ?? t("review.manualEditNote")}</span>
        <div style={{ flex: 1 }} />
        <div onClick={() => live && dirty && !busy && save()} style={{ font: "600 11.5px 'IBM Plex Sans'", color: live && dirty ? "#06121e" : "var(--tx-faint)", background: live && dirty ? "var(--ac)" : "var(--bg-card2)", border: live && dirty ? "none" : "1px solid var(--bd2)", padding: "7px 15px", borderRadius: 7, cursor: live && dirty ? "pointer" : "not-allowed" }}>{busy === "save" ? t("review.saving") : t("review.saveToWorktree")}</div>
      </div>
    </div>
  );
}
