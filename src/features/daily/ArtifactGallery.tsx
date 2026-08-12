import { useEffect, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import { daily as dailyApi, type Artifact, type ScheduleRun } from "@/lib/daily";

// The Daily gallery: what the scheduled runs actually produced, where you review
// it and take it away.
//
// A Daily run is not git work — it writes a report, renders a video, exports a
// chart — so its output is a directory of files rather than a branch to diff.
// The host agent resolves an occurrence id to that directory; nothing here ever
// names a path on the host.
//
// Preview is deliberately narrow. The bytes were written by an agent, so the
// host agent only serves media inline and hands back everything else as a
// download; this component mirrors that split rather than trying to render
// whatever arrives.

const KIND_COLOR: Record<Artifact["kind"], string> = {
  video: "#7c5cff",
  image: "#34d3e0",
  audio: "#e0a83e",
  text: "#5b9fe8",
  file: "#8fa3b8",
};

const VIDEO_GRAD = "linear-gradient(135deg,#1c1530,#2a1d44)";
const AUDIO_GRAD = "linear-gradient(135deg,#241c0e,#332611)";
const IMAGE_GRAD = "linear-gradient(135deg,#0e2630,#123845)";

// Perspective ids map to i18n keys; the words themselves live in the locale.
const PERSPECTIVE_KEY: Record<string, string> = {
  discovery: "daily.perspective.discovery",
  "context-opt": "daily.perspective.contextOpt",
  automation: "daily.perspective.automation",
};

const cardBase: CSSProperties = {
  background: "var(--bg-card)",
  border: "1px solid var(--bd2)",
  borderRadius: 10,
  overflow: "hidden",
  cursor: "pointer",
  display: "flex",
  flexDirection: "column",
};
const cardMeta: CSSProperties = { padding: "10px 11px", display: "flex", flexDirection: "column", gap: 5 };
const cardTitle: CSSProperties = { font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)", lineHeight: 1.35, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" };
const cardSub: CSSProperties = { font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" };
const cornerDot = (bg: string): CSSProperties => ({ position: "absolute", left: 7, top: 7, width: 8, height: 8, borderRadius: 2, background: bg });

function formatSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function shortTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/**
 * A delete that takes two clicks.
 *
 * Removing an artifact takes it off disk and there is no copy — the run that
 * made it is finished. The app deletes templates and tools in one click because
 * those are re-creatable; this is not, so the button asks once. Cheaper than a
 * modal, and it keeps the confirmation next to the thing being confirmed.
 */
function DangerButton({ label, confirmLabel, busyLabel, onConfirm }: {
  label: string; confirmLabel: string; busyLabel: string; onConfirm: () => Promise<void>;
}) {
  const [armed, setArmed] = useState(false);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const id = setTimeout(() => setArmed(false), 4000);
    return () => clearTimeout(id);
  }, [armed]);
  return (
    <div
      onClick={(e) => {
        e.stopPropagation();
        if (busy) return;
        if (!armed) { setArmed(true); return; }
        setBusy(true);
        onConfirm().finally(() => { setBusy(false); setArmed(false); });
      }}
      style={{
        font: "600 11px 'IBM Plex Sans'", cursor: busy ? "default" : "pointer",
        padding: "7px 12px", borderRadius: 7, whiteSpace: "nowrap",
        color: armed ? "#fff" : "var(--red)",
        background: armed ? "var(--red)" : "transparent",
        border: `1px solid ${armed ? "var(--red)" : "var(--tint-red-bd)"}`,
      }}
    >
      {busy ? busyLabel : armed ? confirmLabel : label}
    </div>
  );
}

/** One artifact together with the run it came from — the run id is what makes
 *  it addressable, so the two always travel together. */
interface Item {
  run: ScheduleRun;
  art: Artifact;
}

export function ArtifactGallery({ runs }: { runs: ScheduleRun[] }) {
  const { t } = useTranslation();
  const [items, setItems] = useState<Item[]>([]);
  const drop = (it: Item) => setItems((xs) => xs.filter((x) => !(x.run.id === it.run.id && x.art.path === it.art.path)));
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState<Item | null>(null);

  // Only occurrences that actually launched something have an output directory.
  const producing = runs.filter((r) => r.outputDir);
  const key = producing.map((r) => r.id).join(",");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    Promise.all(
      producing.map((run) =>
        dailyApi
          .artifacts(run.id)
          .then((arts) => arts.map((art) => ({ run, art })))
          .catch(() => [] as Item[]),
      ),
    ).then((groups) => {
      if (cancelled) return;
      setItems(groups.flat());
      setLoading(false);
    });
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  // Group by the schedule's perspective so the gallery keeps its existing
  // shape (discovery / context-opt / automation) rather than a flat pile.
  const groups = new Map<string, Item[]>();
  for (const it of items) {
    const g = it.run.perspective || "automation";
    groups.set(g, [...(groups.get(g) ?? []), it]);
  }

  if (loading) {
    return <div style={{ padding: "18px 20px", font: "400 11px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("daily.loadingArtifacts")}</div>;
  }
  if (items.length === 0) {
    return (
      <div style={{ padding: "18px 20px", display: "flex", flexDirection: "column", gap: 6 }}>
        <span style={{ font: "400 12px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{t("daily.noArtifactsYet")}</span>
        <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
          {t("daily.noArtifactsHint")}
        </span>
      </div>
    );
  }

  return (
    <div style={{ flex: 1, overflowY: "auto", padding: "18px 20px", display: "flex", flexDirection: "column", gap: 22 }}>
      {[...groups.entries()].map(([perspective, list]) => (
        <div key={perspective}>
          <div style={{ display: "flex", alignItems: "center", gap: 9, marginBottom: 12 }}>
            <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "#d39a4e", letterSpacing: "0.5px" }}>
              {PERSPECTIVE_KEY[perspective] ? t(PERSPECTIVE_KEY[perspective]) : perspective}
            </span>
            <div style={{ flex: 1, height: 1, background: "var(--bd)" }} />
            <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("daily.countUnit", { count: list.length })}</span>
          </div>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(4,1fr)", gap: 13 }}>
            {list.map((it) => (
              <ArtifactCard key={`${it.run.id}:${it.art.path}`} item={it} onOpen={() => setOpen(it)} />
            ))}
          </div>
        </div>
      ))}
      {open && <ArtifactModal item={open} onClose={() => setOpen(null)} onDeleted={drop} />}
    </div>
  );
}

/**
 * One run's output, opened from the run history: the files it produced (with
 * download) and its per-stage agent logs.
 *
 * Delivery's equivalent drawer reads a worktree by repo/branch and shows a
 * diff. Daily has neither, which is why this is a separate component rather
 * than a flag on that one.
 */
export function DailyRunDrawer({ run, onClose, onOptimize, onDeleted }: { run: ScheduleRun; onClose: () => void; onOptimize?: () => void; onDeleted?: () => void }) {
  const { t } = useTranslation();
  const [arts, setArts] = useState<Artifact[]>([]);
  const [logs, setLogs] = useState("");
  const [tab, setTab] = useState<"files" | "logs">("files");
  const [open, setOpen] = useState<Item | null>(null);

  useEffect(() => {
    if (run.outputDir) {
      dailyApi.artifacts(run.id).then(setArts).catch(() => setArts([]));
    }
    if (run.runId) {
      import("@/lib/sandbox").then(({ sandbox }) =>
        sandbox
          .runLogs(run.runId)
          .then((r) => setLogs(Object.entries(r.logs).map(([stage, log]) => `── ${stage} ──\n${log}`).join("\n\n")))
          .catch(() => {}),
      );
    }
  }, [run.id, run.runId, run.outputDir]);

  const tabBtn = (t: "files" | "logs", label: string) => (
    <div key={t} onClick={() => setTab(t)} style={{ font: "600 11px 'IBM Plex Sans'", color: tab === t ? "var(--tx)" : "var(--tx-dim)", padding: "6px 12px", background: tab === t ? "var(--bg-tab)" : "transparent", borderRadius: 7, cursor: "pointer" }}>{label}</div>
  );

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 45 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: "72%", minWidth: 720, height: "78%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 12, display: "flex", flexDirection: "column", boxShadow: "0 20px 60px rgba(0,0,0,.5)" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "13px 18px", borderBottom: "1px solid var(--bd)" }}>
          <span style={{ font: "700 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{run.name}</span>
          <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{run.template || t("daily.noTemplateSet")}</span>
          <div style={{ display: "flex", gap: 4, marginLeft: 8 }}>
            {tabBtn("files", t("daily.artifactsCount", { count: arts.length }))}
            {tabBtn("logs", t("daily.agentLogs"))}
          </div>
          <div style={{ flex: 1 }} />
          {run.template && onOptimize && (
            <div onClick={onOptimize} title={t("daily.editPromptsFor", { name: run.template })} style={{ font: "600 10.5px 'IBM Plex Sans'", color: "var(--ac)", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)", cursor: "pointer" }}>
              ⚙ {t("settings.nav.prompt")}
            </div>
          )}
          <DangerButton
            label={t("daily.deleteRun")}
            confirmLabel={t("daily.deleteRunConfirm", { count: arts.length })}
            busyLabel={t("common.loading")}
            onConfirm={() => dailyApi.deleteRun(run.id).then(() => { onDeleted?.(); onClose(); }).catch(() => {})}
          />
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 18px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
        </div>

        {tab === "files" && (
          <div style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: "16px 18px" }}>
            {arts.length === 0 ? (
              <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("daily.scheduleProducedNothing")}</div>
            ) : (
              <div style={{ display: "grid", gridTemplateColumns: "repeat(4,1fr)", gap: 13 }}>
                {arts.map((art) => (
                  <ArtifactCard key={art.path} item={{ run, art }} onOpen={() => setOpen({ run, art })} />
                ))}
              </div>
            )}
          </div>
        )}
        {tab === "logs" && (
          <pre style={{ flex: 1, minHeight: 0, margin: 0, overflow: "auto", padding: "14px 16px", font: "400 11px/1.55 'IBM Plex Mono'", color: "var(--tx3)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
            {logs || t("daily.noLogs")}
          </pre>
        )}
        {open && (
          <ArtifactModal
            item={open}
            onClose={() => setOpen(null)}
            onDeleted={(it) => setArts((xs) => xs.filter((a) => a.path !== it.art.path))}
          />
        )}
      </div>
    </div>
  );
}

function ArtifactCard({ item, onOpen }: { item: Item; onOpen: () => void }) {
  const { art, run } = item;
  const color = KIND_COLOR[art.kind];
  const sub = `${shortTime(art.modTime)} · ${run.name}`;
  return (
    <div onClick={onOpen} style={cardBase} title={art.path}>
      <div style={{ height: 96, position: "relative", background: art.kind === "video" ? VIDEO_GRAD : art.kind === "audio" ? AUDIO_GRAD : art.kind === "image" ? IMAGE_GRAD : "var(--bg-thumb)", display: "flex", alignItems: "center", justifyContent: "center", overflow: "hidden" }}>
        <span style={cornerDot(color)} />
        {art.kind === "image" ? (
          // A real thumbnail: the same URL the preview uses, so what the card
          // shows is what the file is.
          <img src={dailyApi.artifactUrl(run.id, art.path)} alt={art.name} style={{ width: "100%", height: "100%", objectFit: "cover" }} />
        ) : art.kind === "video" ? (
          // A frame of the video itself, for the same reason the image card
          // shows the image: a card that only says "this is a video" cannot be
          // told apart from the next video, and the one thing worth checking at
          // a glance is what was actually produced.
          //
          // `#t=0.1` makes the browser paint a frame instead of a black canvas,
          // and preload="metadata" plus the artifact route's byte-range support
          // means this costs a few KB rather than the whole file.
          <>
            <video
              src={`${dailyApi.artifactUrl(run.id, art.path)}#t=0.1`}
              muted
              playsInline
              preload="metadata"
              style={{ width: "100%", height: "100%", objectFit: "cover" }}
            />
            <div style={{ position: "absolute", inset: 0, display: "flex", alignItems: "center", justifyContent: "center", pointerEvents: "none" }}>
              <div style={{ width: 34, height: 34, borderRadius: "50%", background: "rgba(0,0,0,.45)", display: "flex", alignItems: "center", justifyContent: "center" }}>
                <div style={{ width: 0, height: 0, borderLeft: "10px solid #fff", borderTop: "6px solid transparent", borderBottom: "6px solid transparent", marginLeft: 3 }} />
              </div>
            </div>
          </>
        ) : (
          <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-faint)", textTransform: "uppercase" }}>
            {art.name.split(".").pop()}
          </span>
        )}
        <span style={{ position: "absolute", right: 7, bottom: 7, font: "500 9px 'IBM Plex Mono'", color: "var(--tx3)", background: "rgba(0,0,0,.45)", padding: "2px 5px", borderRadius: 4 }}>
          {formatSize(art.size)}
        </span>
      </div>
      <div style={cardMeta}>
        <div style={cardTitle}>{art.name}</div>
        <div style={cardSub}>{sub}</div>
      </div>
    </div>
  );
}

function ArtifactModal({ item, onClose, onDeleted }: { item: Item; onClose: () => void; onDeleted?: (it: Item) => void }) {
  const { t } = useTranslation();
  const { art, run } = item;
  const src = dailyApi.artifactUrl(run.id, art.path);
  const [text, setText] = useState<string | null>(null);

  useEffect(() => {
    if (art.kind !== "text") return;
    let cancelled = false;
    // Text is fetched rather than embedded: the host agent serves it as an
    // attachment (only media may render inline), so it has to be read as data
    // and displayed as text here.
    fetch(src)
      .then((r) => r.text())
      .then((t) => { if (!cancelled) setText(t); })
      .catch(() => { if (!cancelled) setText(t("daily.readFailed")); });
    return () => { cancelled = true; };
  }, [src, art.kind]);

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.62)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50, padding: 32 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 860, maxWidth: "100%", maxHeight: "100%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, boxShadow: "0 24px 80px rgba(0,0,0,.5)", display: "flex", flexDirection: "column", overflow: "hidden" }}>
        <div style={{ padding: "15px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 11, flex: "none" }}>
          <div style={{ width: 9, height: 9, borderRadius: 2, background: KIND_COLOR[art.kind] }} />
          <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
            <span style={{ font: "700 15px 'IBM Plex Sans'", color: "var(--tx)", letterSpacing: "-0.2px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{art.name}</span>
            <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
              {run.name} · {art.path} · {formatSize(art.size)}
            </span>
          </div>
          <div style={{ flex: 1 }} />
          <a
            href={dailyApi.artifactUrl(run.id, art.path, true)}
            download={art.name}
            style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "7px 14px", borderRadius: 7, textDecoration: "none" }}
          >
            {t("daily.download")}
          </a>
          <DangerButton
            label={t("common.delete")}
            confirmLabel={t("daily.deleteArtifactConfirm")}
            busyLabel={t("common.loading")}
            onConfirm={() => dailyApi.deleteArtifact(run.id, art.path).then(() => { onDeleted?.(item); onClose(); }).catch(() => {})}
          />
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 19px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
        </div>

        <div style={{ minHeight: 0, display: "flex", flexDirection: "column" }}>
          {art.kind === "video" && (
            <video src={src} controls style={{ width: "100%", maxHeight: 460, background: "#000" }} />
          )}
          {art.kind === "audio" && (
            <div style={{ padding: "28px 22px", background: AUDIO_GRAD }}>
              <audio src={src} controls style={{ width: "100%" }} />
            </div>
          )}
          {art.kind === "image" && (
            <div style={{ padding: 18, maxHeight: 500, overflow: "auto", display: "flex", justifyContent: "center", background: "var(--bg-deep)" }}>
              <img src={src} alt={art.name} style={{ maxWidth: "100%", objectFit: "contain" }} />
            </div>
          )}
          {art.kind === "text" && (
            <pre style={{ margin: 0, height: 440, overflow: "auto", padding: "18px 22px", font: "400 11.5px/1.6 'IBM Plex Mono'", color: "var(--tx2)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
              {text ?? t("common.loading")}
            </pre>
          )}
          {art.kind === "file" && (
            <div style={{ padding: "40px 22px", display: "flex", flexDirection: "column", alignItems: "center", gap: 8 }}>
              <span style={{ font: "500 12px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("daily.noPreview")}</span>
              <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)", textAlign: "center", lineHeight: 1.6 }}>
                {t("daily.noPreviewWhy")}
              </span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
