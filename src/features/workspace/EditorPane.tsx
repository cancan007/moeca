// Editor column — tabs, toolbar, code body (highlight | edit), references panel, status bar.
import { useTranslation } from "react-i18next";

import { Highlight } from "./Highlight";
import { baseName, fileIcon, langOf, pluginBadge, importsOf, refsByPath } from "./data";

type View = "hl" | "edit";

export function EditorPane({
  root,
  tabs,
  active,
  dirty,
  content,
  view,
  refsOpen,
  onSelectTab,
  onCloseTab,
  onSetView,
  onToggleRefs,
  onEdit,
  onSave,
  saving,
}: {
  root: string;
  tabs: string[];
  active: string | null;
  dirty: Record<string, boolean>;
  content: string;
  view: View;
  refsOpen: boolean;
  onSelectTab: (path: string) => void;
  onCloseTab: (path: string) => void;
  onSetView: (v: View) => void;
  onToggleRefs: () => void;
  onEdit: (value: string) => void;
  onSave?: () => void;
  saving?: boolean;
}) {
  const { t } = useTranslation();
  const activeDirty = active ? !!dirty[active] : false;
  const refs = active ? refsByPath[active] ?? [] : [];
  const imports = importsOf(content);
  const lineCount = content.split("\n").length;
  const isMd = active ? active.endsWith(".md") : false;
  const plugin = active ? pluginBadge(active) : pluginBadge("");

  const segBtn = (on: boolean) =>
    ({
      cursor: "pointer",
      font: "500 10px 'IBM Plex Mono'",
      padding: "4px 10px",
      borderRadius: 6,
      color: on ? "var(--tx)" : "var(--tx-dim)",
      background: on ? "var(--bg-tab)" : "transparent",
    }) as const;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0, background: "var(--bg-deep)" }}>
      {/* tabs */}
      <div style={{ flex: "none", height: 38, display: "flex", alignItems: "stretch", background: "var(--bg-panel)", borderBottom: "1px solid var(--bd)", overflowX: "auto" }}>
        {tabs.map((path) => {
          const on = path === active;
          return (
            <div
              key={path}
              onClick={() => onSelectTab(path)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 7,
                padding: "0 12px",
                cursor: "pointer",
                flex: "none",
                font: "500 11.5px 'IBM Plex Mono'",
                color: on ? "var(--tx)" : "var(--tx-dim)",
                background: on ? "var(--bg-deep)" : "var(--bg-panel)",
                borderRight: "1px solid var(--bd)",
                borderTop: on ? "2px solid var(--ac)" : "2px solid transparent",
              }}
            >
              <div style={{ width: 7, height: 7, borderRadius: 2, background: fileIcon(path), flex: "none" }} />
              <span>{baseName(path)}</span>
              {dirty[path] && <span style={{ color: "#d39a4e", fontSize: 10 }}>●</span>}
              <span
                onClick={(e) => { e.stopPropagation(); onCloseTab(path); }}
                style={{ color: "var(--tx-mut)", fontSize: 13, padding: "0 2px", marginLeft: 2 }}
              >
                ✕
              </span>
            </div>
          );
        })}
      </div>

      {/* toolbar */}
      <div style={{ flex: "none", padding: "7px 14px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 9 }}>
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
          {active ?? "—"}
        </span>
        <span style={{ display: "flex", alignItems: "center", gap: 5, font: "500 9px 'IBM Plex Mono'", color: plugin.color, background: plugin.bg, border: `1px solid ${plugin.bd}`, padding: "3px 8px", borderRadius: 6 }}>
          <div style={{ width: 5, height: 5, borderRadius: "50%", background: plugin.color }} />
          {plugin.label}
        </span>
        <div style={{ display: "flex", gap: 2, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: 2 }}>
          <div onClick={() => onSetView("hl")} style={segBtn(view === "hl")}>{t("workspace.highlight")}</div>
          <div onClick={() => onSetView("edit")} style={segBtn(view === "edit")}>{t("common.edit")}</div>
        </div>
        <div
          onClick={onToggleRefs}
          style={{ cursor: "pointer", font: "500 10px 'IBM Plex Mono'", padding: "5px 10px", borderRadius: 7, background: refsOpen ? "var(--tint-active)" : "var(--bg-card2)", border: `1px solid ${refsOpen ? "var(--tint-active-bd)" : "var(--bd2)"}`, color: refsOpen ? "var(--tx)" : "var(--tx3)" }}
        >
          {t("workspace.refs", { count: refs.length })}
        </div>
      </div>

      {/* editor body */}
      <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
        {view === "hl" ? (
          <div style={{ flex: 1, minWidth: 0, overflow: "auto", padding: "14px 16px", background: "var(--bg-deep)" }}>
            <Highlight code={content} plain={isMd} />
          </div>
        ) : (
          <textarea
            value={content}
            onChange={(e) => onEdit(e.target.value)}
            spellCheck={false}
            style={{ flex: 1, minWidth: 0, minHeight: 0, resize: "none", border: "none", outline: "none", background: "var(--bg-deep)", color: "var(--tx2)", fontFamily: "'IBM Plex Mono',monospace", fontSize: 12.5, lineHeight: 1.9, padding: "16px 20px", tabSize: 2 }}
          />
        )}

        {refsOpen && (
          <div style={{ width: 222, flex: "none", borderLeft: "1px solid var(--bd)", background: "var(--bg-panel)", overflowY: "auto", padding: 13, display: "flex", flexDirection: "column", gap: 13 }}>
            <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px" }}>{t("workspace.references")}</span>

            {refs.length > 0 ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
                {refs.map((r) => (
                  <div key={r.name} style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "9px 10px", display: "flex", flexDirection: "column", gap: 4 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                      <div style={{ width: 6, height: 6, borderRadius: 2, background: "#34d3e0", flex: "none" }} />
                      <span style={{ font: "600 10.5px 'IBM Plex Mono'", color: "#34d3e0" }}>{r.name}</span>
                      <span style={{ marginLeft: "auto", font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{r.kind}</span>
                    </div>
                    <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{r.def}</span>
                    <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{r.use}</span>
                  </div>
                ))}
              </div>
            ) : (
              <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)", lineHeight: 1.5 }}>
                {t("workspace.noSymbols")}
              </span>
            )}

            {imports.length > 0 && (
              <div style={{ display: "flex", flexDirection: "column", gap: 7, paddingTop: 11, borderTop: "1px solid var(--bd-soft)" }}>
                <span style={{ font: "600 9px 'IBM Plex Mono'", color: "#b08ad9", letterSpacing: "0.5px" }}>{t("workspace.imports")}</span>
                {imports.map((p) => (
                  <div key={p} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                    <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="#b08ad9" strokeWidth="1.6">
                      <path d="M6 3H3v10h10v-3M9 7l5-5M10 2h4v4" />
                    </svg>
                    <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{p}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div>

      {/* status bar */}
      <div style={{ flex: "none", height: 28, background: "var(--bg-panel)", borderTop: "1px solid var(--bd)", display: "flex", alignItems: "center", padding: "0 14px", gap: 14, font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
        <span style={{ display: "flex", alignItems: "center", gap: 5 }}>
          <svg width="11" height="11" viewBox="0 0 14 14" fill="none" stroke="var(--tx-dim)" strokeWidth="1.5">
            <path d="M4 2v6a2 2 0 0 0 2 2h4M4 2a1.5 1.5 0 1 1 0 .01M10 10a1.5 1.5 0 1 1 0 .01M4 8V4" />
          </svg>
          {root}
        </span>
        <span>{t("workspace.lineCount", { count: lineCount })}</span>
        <span>{active ? langOf(active) : "—"}</span>
        <div style={{ flex: 1 }} />
        {activeDirty && <span style={{ color: "#d39a4e" }}>● {t("workspace.unsaved")}</span>}
        <div
          onClick={() => onSave && activeDirty && !saving && onSave()}
          style={{ font: "600 9.5px 'IBM Plex Sans'", color: onSave && activeDirty ? "#06121e" : "var(--tx-faint)", background: onSave && activeDirty ? "var(--ac)" : "var(--bg-card2)", border: onSave && activeDirty ? "none" : "1px solid var(--bd2)", padding: "4px 12px", borderRadius: 6, cursor: onSave && activeDirty ? "pointer" : "default" }}
        >
          {saving ? t("review.saving") : t("common.save")}
        </div>
      </div>
    </div>
  );
}
