import { useState } from "react";
import { useTranslation } from "react-i18next";

type Line =
  | { k: "cmd"; text: string }
  | { k: "out"; text: string; color?: string }
  | { k: "ok"; text: string }
  | { k: "warn"; text: string }
  | { k: "caret" };

type Pane = { branch: string; agent: string; lines: Line[] };

type Tab = { id: string; label: string; dot: string; left: Pane; right: Pane };

const tabs: Tab[] = [
  {
    id: "web-app",
    label: "web-app",
    dot: "var(--ac)",
    left: {
      branch: "feat/batch-index",
      agent: "Builder · sonnet",
      lines: [
        { k: "cmd", text: "pnpm test src/indexer" },
        { k: "out", text: "RUN  v1.6.0  /repo/web-app" },
        { k: "out", text: " ✓ indexer.ts (12 tests) 340ms", color: "var(--green)" },
        { k: "out", text: " ✓ chunk() splits into batches of 500", color: "var(--tx3)" },
        { k: "ok", text: "Test Files  1 passed (1)" },
        { k: "ok", text: "     Tests  12 passed (12)" },
        { k: "cmd", text: "git add -A && git commit -m \"batch index writes\"" },
        { k: "out", text: "[feat/batch-index a1f9c22] batch index writes", color: "var(--tx3)" },
        { k: "out", text: " 2 files changed, 14 insertions(+), 3 deletions(-)", color: "var(--tx-dim)" },
        { k: "caret" },
      ],
    },
    right: {
      branch: "fix/token-budget",
      agent: "Builder · sonnet",
      lines: [
        { k: "cmd", text: "rg \"token / day\" src --stats" },
        { k: "out", text: "src/features/delivery/Delivery.tsx:76", color: "var(--tx3)" },
        { k: "out", text: "1 matches · 1 files · 0.004s", color: "var(--tx-dim)" },
        { k: "cmd", text: "pnpm build" },
        { k: "out", text: "vite v5.4.2 building for production…" },
        { k: "warn", text: "! chunk larger than 500 kB after minification" },
        { k: "ok", text: "✓ built in 4.81s" },
        { k: "caret" },
      ],
    },
  },
  {
    id: "api",
    label: "api",
    dot: "#67c9a4",
    left: {
      branch: "feat/rate-limit",
      agent: "Builder · sonnet",
      lines: [
        { k: "cmd", text: "go test ./internal/limiter/..." },
        { k: "out", text: "ok  orchestra/internal/limiter  0.213s", color: "var(--green)" },
        { k: "caret" },
      ],
    },
    right: {
      branch: "chore/migrate-db",
      agent: "Builder · sonnet",
      lines: [
        { k: "cmd", text: "migrate up" },
        { k: "out", text: "20260708_add_worktrees ... done", color: "var(--tx3)" },
        { k: "ok", text: "schema now at version 42" },
        { k: "caret" },
      ],
    },
  },
  {
    id: "infra",
    label: "infra",
    dot: "var(--amber)",
    left: {
      branch: "feat/tauri-shell",
      agent: "Builder · sonnet",
      lines: [
        { k: "cmd", text: "cargo check" },
        { k: "out", text: "  Compiling orchestra v0.1.0", color: "var(--tx3)" },
        { k: "ok", text: "Finished dev [unoptimized] in 6.2s" },
        { k: "caret" },
      ],
    },
    right: {
      branch: "feat/notarize",
      agent: "Builder · sonnet",
      lines: [
        { k: "cmd", text: "xcrun notarytool submit Orchestra.dmg" },
        { k: "out", text: "Successfully uploaded file", color: "var(--tx3)" },
        { k: "warn", text: "status: In Progress" },
        { k: "caret" },
      ],
    },
  },
];

function lineColor(l: Line): string {
  switch (l.k) {
    case "cmd":
      return "var(--tx2)";
    case "ok":
      return "#67c9a4";
    case "warn":
      return "var(--amber)";
    case "out":
      return l.color ?? "var(--tx3)";
    default:
      return "var(--tx3)";
  }
}

function PaneView({ pane }: { pane: Pane }) {
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0, background: "var(--bg-deep)" }}>
      <div style={{ flex: "none", height: 34, display: "flex", alignItems: "center", gap: 8, padding: "0 14px", borderBottom: "1px solid var(--bd)" }}>
        <svg width="12" height="12" viewBox="0 0 14 14" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5"><path d="M4 2v6a2 2 0 0 0 2 2h4M4 2a1.5 1.5 0 1 1 0 .01M10 10a1.5 1.5 0 1 1 0 .01M4 8V4" /></svg>
        <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx3)" }}>{pane.branch}</span>
        <span className="oc-active-dot" style={{ width: 6, height: 6, borderRadius: "50%", background: "#3fbf8f" }} />
        <div style={{ flex: 1 }} />
        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{pane.agent}</span>
      </div>
      <div style={{ flex: 1, overflowY: "auto", padding: "14px 16px", fontFamily: "'IBM Plex Mono',monospace", fontSize: 11.5, lineHeight: 1.85 }}>
        {pane.lines.map((l, i) =>
          l.k === "caret" ? (
            <div key={i} style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span style={{ color: "var(--ac)" }}>{'❯'}</span>
              <span className="oc-caret" style={{ display: "inline-block", width: 7, height: 14, background: "var(--tx3)", verticalAlign: "middle" }} />
            </div>
          ) : l.k === "cmd" ? (
            <div key={i} style={{ display: "flex", gap: 8 }}>
              <span style={{ color: "var(--ac)", flex: "none" }}>{'❯'}</span>
              <span style={{ color: lineColor(l) }}>{l.text}</span>
            </div>
          ) : (
            <div key={i} style={{ color: lineColor(l), whiteSpace: "pre-wrap" }}>{l.text}</div>
          )
        )}
      </div>
    </div>
  );
}

export function Terminal() {
  const { t } = useTranslation();
  const [active, setActive] = useState(tabs[0].id);
  const current = tabs.find((t) => t.id === active) ?? tabs[0];

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, background: "var(--bg-app)" }}>
      {/* tab bar */}
      <div style={{ flex: "none", height: 44, display: "flex", alignItems: "flex-end", gap: 3, padding: "0 14px", borderBottom: "1px solid var(--bd)", background: "var(--bg-panel)" }}>
        {tabs.map((tb) => {
          const on = tb.id === active;
          return (
            <div
              key={tb.id}
              onClick={() => setActive(tb.id)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 7,
                marginBottom: on ? -1 : 4,
                padding: "7px 12px",
                borderRadius: on ? "7px 7px 0 0" : 7,
                border: on ? "1px solid var(--bd)" : "1px solid transparent",
                borderBottom: on ? "1px solid var(--bg-deep)" : "1px solid transparent",
                background: on ? "var(--bg-deep)" : "transparent",
                cursor: "pointer",
                font: "500 11px 'IBM Plex Mono'",
                color: on ? "var(--tx)" : "var(--tx-dim)",
              }}
            >
              <div style={{ width: 7, height: 7, borderRadius: "50%", background: tb.dot }} />
              <span>{tb.label}</span>
            </div>
          );
        })}
        <div style={{ display: "flex", alignItems: "center", justifyContent: "center", width: 28, height: 28, marginBottom: 4, marginLeft: 4, borderRadius: 7, border: "1px dashed var(--bd2)", color: "var(--tx-dim)", fontSize: 15, cursor: "pointer" }}>+</div>
        <div style={{ flex: 1 }} />
        <div style={{ display: "flex", alignItems: "center", gap: 7, marginBottom: 8, padding: "5px 10px", background: "var(--tint-green)", border: "1px solid var(--tint-green-bd)", borderRadius: 7 }}>
          <div style={{ width: 6, height: 6, borderRadius: "50%", background: "#3fbf8f", boxShadow: "0 0 6px #3fbf8f" }} />
          <span style={{ font: "500 10px 'IBM Plex Mono'", color: "#67c9a4" }}>{t("terminal.worktreesRunning", { count: 2 })}</span>
        </div>
      </div>

      {/* split panes */}
      <div style={{ flex: 1, display: "flex", minHeight: 0, gap: 1, background: "var(--bd)" }}>
        <PaneView pane={current.left} />
        <PaneView pane={current.right} />
      </div>

      {/* input bar */}
      <div style={{ flex: "none", padding: "11px 16px", borderTop: "1px solid var(--bd)", background: "var(--bg-panel)", display: "flex", alignItems: "center", gap: 10 }}>
        <span style={{ font: "600 11px 'IBM Plex Mono'", color: "var(--ac)" }}>{'❯'}</span>
        <span style={{ font: "400 11.5px 'IBM Plex Mono'", color: "var(--tx-dim)", flex: 1 }}>{t("terminal.inputPlaceholder")}</span>
        <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "3px 8px", borderRadius: 5 }}>⏎ send</span>
      </div>
    </div>
  );
}
