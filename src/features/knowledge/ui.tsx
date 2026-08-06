import type { CSSProperties, ReactNode } from "react";

// Small shared pieces of the Knowledge screen. They exist to keep the three
// modes visually identical where they do the same thing — a rail heading is a
// rail heading whether it is listing groups or node types.

export const railHeading: CSSProperties = {
  font: "600 9.5px 'IBM Plex Mono'",
  letterSpacing: ".1em",
  color: "var(--tx-faint)",
};

export const monoMeta: CSSProperties = {
  font: "500 9.5px 'IBM Plex Mono'",
  color: "var(--tx-dim)",
};

export const panel: CSSProperties = {
  background: "var(--bg-panel)",
  display: "flex",
  flexDirection: "column",
  overflowY: "auto",
};

export function Swatch({ color, size = 8 }: { color: string; size?: number }) {
  return (
    <div
      style={{
        width: size,
        height: size,
        borderRadius: 2.5,
        background: color || "var(--tx3)",
        flex: "none",
      }}
    />
  );
}

export function Section({
  title,
  meta,
  children,
  action,
}: {
  title: string;
  meta?: ReactNode;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div
      style={{
        padding: "12px 14px",
        borderBottom: "1px solid var(--bd-soft)",
        display: "flex",
        flexDirection: "column",
        gap: 8,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
        <span style={railHeading}>{title}</span>
        <div style={{ flex: 1 }} />
        {meta ? <span style={monoMeta}>{meta}</span> : null}
        {action}
      </div>
      {children}
    </div>
  );
}

export function Button({
  onClick,
  children,
  tone = "quiet",
  disabled,
}: {
  onClick?: () => void;
  children: ReactNode;
  tone?: "quiet" | "accent" | "danger";
  disabled?: boolean;
}) {
  const color =
    tone === "accent" ? "var(--ac)" : tone === "danger" ? "var(--red)" : "var(--tx3)";
  return (
    <div
      onClick={disabled ? undefined : onClick}
      style={{
        font: "600 10px 'IBM Plex Sans'",
        color: disabled ? "var(--tx-dim)" : color,
        border: `1px solid ${tone === "accent" ? "var(--tint-active-bd)" : "var(--bd2)"}`,
        background: tone === "accent" ? "var(--tint-accent)" : "var(--bg-deep)",
        padding: "5px 10px",
        borderRadius: 6,
        cursor: disabled ? "default" : "pointer",
        opacity: disabled ? 0.5 : 1,
        whiteSpace: "nowrap",
        userSelect: "none",
      }}
    >
      {children}
    </div>
  );
}

export function TextInput({
  value,
  onChange,
  placeholder,
  onEnter,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  onEnter?: () => void;
}) {
  return (
    <input
      value={value}
      placeholder={placeholder}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter" && onEnter) onEnter();
      }}
      style={{
        flex: 1,
        minWidth: 0,
        background: "var(--bg-deep)",
        border: "1px solid var(--bd2)",
        borderRadius: 6,
        padding: "6px 8px",
        font: "400 11px 'IBM Plex Sans'",
        color: "var(--tx)",
        outline: "none",
      }}
    />
  );
}

/** Notice carries a state the user has to understand before trusting the
 *  screen — an unreachable service, an index that has not been built. It is
 *  deliberately not a toast: these conditions persist. */
export function Notice({
  tone,
  children,
}: {
  tone: "warn" | "error" | "info";
  children: ReactNode;
}) {
  const colors = {
    warn: { fg: "#e0c08e", bg: "var(--tint-amber)", bd: "#43331c" },
    error: { fg: "#e5a2a2", bg: "var(--tint-red)", bd: "var(--tint-red-bd)" },
    info: { fg: "var(--tx3)", bg: "var(--bg-card)", bd: "var(--bd2)" },
  }[tone];
  return (
    <div
      style={{
        display: "flex",
        gap: 8,
        alignItems: "flex-start",
        background: colors.bg,
        border: `1px solid ${colors.bd}`,
        borderRadius: 8,
        padding: "9px 11px",
        font: "400 10.5px 'IBM Plex Sans'",
        color: colors.fg,
        lineHeight: 1.65,
      }}
    >
      {children}
    </div>
  );
}

/** EmptyState fills a pane that has nothing to show yet, and says what would
 *  put something there. */
export function EmptyState({ title, hint }: { title: string; hint: string }) {
  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        gap: 9,
        padding: 30,
        textAlign: "center",
      }}
    >
      <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx3)" }}>{title}</span>
      <span
        style={{
          font: "400 10.5px 'IBM Plex Sans'",
          color: "var(--tx-dim)",
          lineHeight: 1.7,
          maxWidth: 340,
        }}
      >
        {hint}
      </span>
    </div>
  );
}

export function selectableRow(active: boolean): CSSProperties {
  return {
    display: "flex",
    alignItems: "center",
    gap: 7,
    padding: "6px 8px",
    borderRadius: 6,
    cursor: "pointer",
    userSelect: "none",
    background: active ? "var(--tint-active)" : "transparent",
    border: `1px solid ${active ? "var(--tint-active-bd)" : "transparent"}`,
  };
}

export function pillStyle(active: boolean, color = "var(--ac)"): CSSProperties {
  return {
    font: "600 10px 'IBM Plex Sans'",
    padding: "4px 9px",
    borderRadius: 6,
    cursor: "pointer",
    userSelect: "none",
    whiteSpace: "nowrap",
    color: active ? color : "var(--tx-dim)",
    background: active ? "var(--bg-deep)" : "transparent",
    border: `1px solid ${active ? color : "var(--bd2)"}`,
  };
}
