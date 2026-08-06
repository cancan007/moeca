import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { hostagent } from "@/lib/hostagent";
import type { Artifact } from "@/lib/daily";
import type { DeliveryTask } from "@/store/useStore";

// What the task produced, as opposed to what it changed.
//
// The diff tab answers "what changed" and is the right instrument for code. It
// is the wrong one for an image: a generated PNG appears there as "binary file
// changed", which is accurate and tells the reviewer nothing. Now that an agent
// can generate images, audio and video into its worktree, that output needs the
// treatment Daily's gallery already gives it — shown, played, listened to.
//
// Preview is deliberately narrow, and mirrors what the host will actually
// serve: media renders inline, everything else is a download. The bytes were
// written by an agent, so rendering an .html or .svg here would run its script
// in the origin that talks to the loopback services.

const KIND_KEY: Record<Artifact["kind"], string> = {
  image: "review.kind.image",
  video: "review.kind.video",
  audio: "review.kind.audio",
  text: "review.kind.text",
  file: "review.kind.file",
};

const KIND_COLOR: Record<Artifact["kind"], string> = {
  image: "#34d3e0",
  video: "#b08ad9",
  audio: "#e0a83e",
  text: "#5b9fe8",
  file: "#8fa3b8",
};

function formatSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

export function ArtifactsPane({ task }: { task: DeliveryTask }) {
  const { t } = useTranslation();
  const [items, setItems] = useState<Artifact[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  // Same pair the diff and source panes use: the board's project name is the
  // host agent's repo name.
  const repo = task.project;
  const branch = task.branch;

  useEffect(() => {
    let cancelled = false;
    setItems(null);
    setError(null);
    hostagent
      .artifacts(repo, branch)
      .then((a) => !cancelled && setItems(a))
      .catch((e) => !cancelled && setError(e instanceof Error ? e.message : String(e)));
    return () => {
      cancelled = true;
    };
  }, [repo, branch]);

  // Media first: the reason this tab exists is the output a diff cannot show.
  const media = (items ?? []).filter((a) => a.kind === "image" || a.kind === "video" || a.kind === "audio");
  const rest = (items ?? []).filter((a) => !media.includes(a));
  const current = items?.find((a) => a.path === selected) ?? media[0] ?? null;

  if (error) {
    return (
      <div style={{ font: "400 11px 'IBM Plex Mono'", color: "var(--red)", padding: 16 }}>
        {t("review.artifactsFailed")}: {error}
      </div>
    );
  }
  if (!items) {
    return <div style={{ font: "400 11px 'IBM Plex Mono'", color: "var(--tx-faint)", padding: 16 }}>{t("common.loading")}</div>;
  }
  if (items.length === 0) {
    return (
      <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: 16, lineHeight: 1.7 }}>
        {t("review.noArtifacts")}
        <br />
        {t("review.noArtifactsHint")}
      </div>
    );
  }

  return (
    <div style={{ display: "flex", height: "100%", minHeight: 0 }}>
      <div style={{ width: 240, flex: "none", borderRight: "1px solid var(--bd)", overflowY: "auto", padding: 10, display: "flex", flexDirection: "column", gap: 5 }}>
        {[...media, ...rest].map((a) => {
          const on = current?.path === a.path;
          return (
            <div
              key={a.path}
              onClick={() => setSelected(a.path)}
              style={{ display: "flex", alignItems: "center", gap: 7, cursor: "pointer", background: on ? "var(--tint-active)" : "var(--bg-inset2)", border: `1px solid ${on ? "var(--tint-active-bd)" : "var(--bd3)"}`, borderRadius: 7, padding: "7px 9px" }}
            >
              <span style={{ width: 7, height: 7, borderRadius: 2, background: KIND_COLOR[a.kind], flex: "none" }} />
              <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx2)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={a.path}>
                {a.path}
              </span>
              <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>{formatSize(a.size)}</span>
            </div>
          );
        })}
      </div>

      <div style={{ flex: 1, minWidth: 0, overflowY: "auto", padding: 16, display: "flex", flexDirection: "column", gap: 12 }}>
        {current && (
          <>
            <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
              <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: KIND_COLOR[current.kind], background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 6px", borderRadius: 4 }}>
                {t(KIND_KEY[current.kind])}
              </span>
              <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{current.name}</span>
              <div style={{ flex: 1 }} />
              <a
                href={hostagent.artifactUrl(repo, branch, current.path, true)}
                download={current.name}
                style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--ac)", textDecoration: "none", border: "1px solid var(--tint-active-bd)", background: "var(--tint-active)", borderRadius: 7, padding: "5px 11px" }}
              >
                {t("daily.download")}
              </a>
            </div>
            <Preview repo={repo} branch={branch} artifact={current} />
          </>
        )}
      </div>
    </div>
  );
}

function Preview({ repo, branch, artifact }: { repo: string; branch: string; artifact: Artifact }) {
  const { t } = useTranslation();
  const src = hostagent.artifactUrl(repo, branch, artifact.path);
  const frame: React.CSSProperties = { maxWidth: "100%", borderRadius: 8, border: "1px solid var(--bd2)", background: "var(--bg-deep)" };
  switch (artifact.kind) {
    case "image":
      return <img src={src} alt={artifact.name} style={{ ...frame, maxHeight: 460, objectFit: "contain" }} />;
    case "video":
      return <video src={src} controls style={{ ...frame, maxHeight: 460 }} />;
    case "audio":
      return <audio src={src} controls style={{ width: "100%" }} />;
    default:
      // Text is listed and downloadable but not rendered: the source tab is
      // where a reviewer reads files, and it reads them as text, not as markup.
      return (
        <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.7 }}>
          {t("review.cannotPreview")}
        </div>
      );
  }
}
