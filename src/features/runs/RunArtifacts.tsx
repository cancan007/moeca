import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { hostagent } from "@/lib/hostagent";
import { sandbox as sandboxApi } from "@/lib/sandbox";

// A run whose artifacts we can show — from a scheduled occurrence or a manual
// Delivery run. Files come from the worktree (repo/branch); logs from the
// sandbox container (bare) or the orchestrator run (template DAG, per-stage).
export interface RunRecord {
  id: string | number;
  name: string;
  repo: string;
  branch: string;
  containerId: string;
  runId: string;
  template: string;
}

// RunArtifacts — the real output of an agent run: the files it produced in its
// worktree, and the A2A session logs from its sandbox/run.
export function RunArtifacts({ run, onClose, onOptimize }: { run: RunRecord; onClose: () => void; onOptimize?: () => void }) {
  const { t } = useTranslation();
  const [files, setFiles] = useState<string[]>([]);
  const [active, setActive] = useState<string | null>(null);
  const [content, setContent] = useState("");
  const [logs, setLogs] = useState("");
  const [tab, setTab] = useState<"files" | "logs">("files");
  const [err, setErr] = useState<string | null>(null);

  const open = async (p: string) => {
    setActive(p);
    try {
      setContent(await hostagent.file(run.repo, run.branch, p));
    } catch {
      setContent(t("daily.readFailed"));
    }
  };

  useEffect(() => {
    hostagent
      .files(run.repo, run.branch)
      .then((fs) => {
        setFiles(fs);
        if (fs[0]) open(fs[0]);
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
    // Every run is a template run, so stage logs come from /run/logs. They are
    // archived when each stage ends, so this still works after the containers
    // are gone.
    if (run.runId) {
      sandboxApi
        .runLogs(run.runId)
        .then((r) => setLogs(Object.entries(r.logs).map(([stage, log]) => `── ${stage} ──\n${log}`).join("\n\n")))
        .catch(() => {});
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run.id]);

  const tabBtn = (t: "files" | "logs", label: string) => (
    <div onClick={() => setTab(t)} style={{ font: "600 11px 'IBM Plex Sans'", color: tab === t ? "var(--tx)" : "var(--tx-dim)", padding: "6px 12px", background: tab === t ? "var(--bg-tab)" : "transparent", borderRadius: 7, cursor: "pointer" }}>{label}</div>
  );

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 45 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: "72%", minWidth: 720, height: "78%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 12, display: "flex", flexDirection: "column", boxShadow: "0 20px 60px rgba(0,0,0,.5)" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "13px 18px", borderBottom: "1px solid var(--bd)" }}>
          <span style={{ font: "700 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{run.name}</span>
          <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{run.repo} · {run.branch}</span>
          <div style={{ display: "flex", gap: 4, marginLeft: 8 }}>
            {tabBtn("files", t("runs.artifactFiles", { count: files.length }))}
            {tabBtn("logs", t("daily.agentLogs"))}
          </div>
          <div style={{ flex: 1 }} />
          {run.template && onOptimize && (
            <div onClick={onOptimize} title={t("daily.editPromptsFor", { name: run.template })} style={{ font: "600 10.5px 'IBM Plex Sans'", color: "var(--ac)", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)", cursor: "pointer" }}>
              ⚙ {t("settings.nav.prompt")}
            </div>
          )}
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 18px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
        </div>
        {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)", padding: "8px 18px" }}>{err}</div>}

        {tab === "files" && (
          <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
            <div style={{ width: 240, flex: "none", borderRight: "1px solid var(--bd)", overflowY: "auto", padding: "8px 6px" }}>
              {files.length === 0 && <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "6px 8px" }}>{t("runs.noFiles")}</div>}
              {files.map((f) => (
                <div key={f} onClick={() => open(f)} style={{ font: "500 11px 'IBM Plex Mono'", color: active === f ? "var(--tx)" : "var(--tx3)", padding: "5px 8px", borderRadius: 6, background: active === f ? "var(--tint-active)" : "transparent", cursor: "pointer", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{f}</div>
              ))}
            </div>
            <pre style={{ flex: 1, minWidth: 0, margin: 0, overflow: "auto", padding: "14px 16px", font: "400 11.5px/1.6 'IBM Plex Mono'", color: "var(--tx2)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{content}</pre>
          </div>
        )}
        {tab === "logs" && (
          <pre style={{ flex: 1, minHeight: 0, margin: 0, overflow: "auto", padding: "14px 16px", font: "400 11px/1.55 'IBM Plex Mono'", color: "var(--tx3)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{logs || t("daily.noLogs")}</pre>
        )}
      </div>
    </div>
  );
}
