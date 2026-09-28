import type { CSSProperties } from "react";
import type { Artifact } from "@/lib/daily";
import type { SoloAgent } from "@/lib/templates";

// Shared pieces of the Chat screen. Small enough to be obvious, shared because
// the thread, the rail, the composer and the artifacts panel all draw the same
// agent avatars and the same file badges, and three copies of a circle is how
// they stop matching.

/** An agent's initial on its own colour. The colour is the agent's `dot`, which
 *  is what identifies it everywhere else in the app, so the same agent is the
 *  same colour in a chat as it is in a template graph. */
export function Avatar({ agent, name, size = 30 }: { agent?: SoloAgent; name?: string; size?: number }) {
  const label = (agent?.name ?? name ?? "?").trim();
  const color = agent?.dot || "var(--avatar-mut)";
  return (
    <div
      style={{
        width: size, height: size, flex: "none", borderRadius: "50%",
        background: color, display: "flex", alignItems: "center", justifyContent: "center",
        font: `600 ${Math.round(size * 0.4)}px 'IBM Plex Sans'`, color: "#06121e",
      }}
    >
      {label.slice(0, 1).toUpperCase()}
    </div>
  );
}

const KIND_COLOR: Record<Artifact["kind"], string> = {
  video: "#7c5cff",
  image: "#34d3e0",
  audio: "#e0a83e",
  text: "#5b9fe8",
  pdf: "#e0654e",
  file: "#8fa3b8",
};

export function extOf(name: string): string {
  const i = name.lastIndexOf(".");
  return i > 0 ? name.slice(i + 1).toUpperCase().slice(0, 4) : "—";
}

export function kindColor(kind: Artifact["kind"]): string {
  return KIND_COLOR[kind] ?? KIND_COLOR.file;
}

/** The little coloured extension tag on a file row. */
export function ExtBadge({ name, kind, size = 30 }: { name: string; kind: Artifact["kind"]; size?: number }) {
  return (
    <div
      style={{
        width: size, height: size, flex: "none", borderRadius: 8,
        background: "var(--bg-card2)", border: `1px solid ${kindColor(kind)}33`,
        display: "flex", alignItems: "center", justifyContent: "center",
        font: "600 8.5px 'IBM Plex Mono'", color: kindColor(kind), letterSpacing: "0.03em",
      }}
    >
      {extOf(name)}
    </div>
  );
}

/** A round icon button, as used for the rail toggle, the panel toggle and the
 *  small dismissals. */
export function iconButton(active = false): CSSProperties {
  return {
    width: 30, height: 30, borderRadius: "50%", flex: "none",
    display: "flex", alignItems: "center", justifyContent: "center",
    cursor: "pointer", color: active ? "var(--tx2)" : "var(--tx-dim)",
    background: active ? "var(--bg-card)" : "transparent",
  };
}

export const pillButton: CSSProperties = {
  font: "500 11px 'IBM Plex Sans'", color: "var(--tx-dim)",
  padding: "6px 10px", borderRadius: 14, cursor: "pointer", whiteSpace: "nowrap",
};

export const primaryPill: CSSProperties = {
  font: "600 11px 'IBM Plex Sans'", color: "var(--tx)", background: "var(--bg-card2)",
  padding: "6px 13px", borderRadius: 14, cursor: "pointer", whiteSpace: "nowrap",
};

export const searchBox: CSSProperties = {
  display: "flex", alignItems: "center", gap: 8,
  background: "var(--bg-card)", borderRadius: 9, padding: "8px 11px",
};

export const searchInput: CSSProperties = {
  flex: 1, minWidth: 0, background: "transparent", border: "none", outline: "none",
  font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx)",
};

export function SearchIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="var(--tx-faint)" strokeWidth="1.6">
      <circle cx="7" cy="7" r="4.5" />
      <path d="m10.5 10.5 3 3" />
    </svg>
  );
}

export function ReplyIcon({ color = "var(--tx-dim)" }: { color?: string }) {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke={color} strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6.5 4 2.5 8l4 4M3 8h6.5a4 4 0 0 1 4 4v.5" />
    </svg>
  );
}

export function PanelIcon({ mirrored = false }: { mirrored?: boolean }) {
  return (
    <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
      <rect x="2" y="2.5" width="12" height="11" rx="2" />
      <path d={mirrored ? "M10 2.5v11" : "M6 2.5v11"} />
    </svg>
  );
}

/** Colour for a turn's outcome, matching the palette the run views use. */
export function statusColor(status: string | undefined): string {
  if (status === "running") return "var(--ac)";
  if (status === "done") return "#67c9a4";
  if (status === "failed") return "var(--red)";
  if (status === "stopped") return "#e0a83e";
  if (status === "empty") return "#e0a83e";
  return "var(--tx-faint)";
}
