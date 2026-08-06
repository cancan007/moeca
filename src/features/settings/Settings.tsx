import { useState } from "react";
import { useTranslation } from "react-i18next";
import { PromptPanel } from "./PromptPanel";
import { HistoryPanel } from "./HistoryPanel";
import { RagPanel } from "./RagPanel";
import { AgentsPanel } from "./AgentsPanel";
import { ToolsPanel } from "./ToolsPanel";
import { SourcesPanel } from "./SourcesPanel";
import { RepositoriesPanel } from "./RepositoriesPanel";
import { SandboxPanel } from "./SandboxPanel";
import { ProxyPanel } from "./ProxyPanel";

type Tab = "prompt" | "history" | "rag" | "agents" | "tools" | "sources" | "repos" | "sandbox" | "proxy";

interface NavItem {
  id: Tab;
  dot: string;
}

const optimization: NavItem[] = [
  { id: "prompt", dot: "#4f9dff" },
  { id: "history", dot: "#34d3e0" },
  { id: "rag", dot: "#67c9a4" },
  { id: "agents", dot: "#b08ad9" },
  { id: "tools", dot: "#3fbf8f" },
  { id: "sources", dot: "#b08ad9" },
  { id: "repos", dot: "#4f9dff" },
];

const security: NavItem[] = [
  { id: "sandbox", dot: "#e0a83e" },
  { id: "proxy", dot: "#e0654e" },
];

// wide layouts (rag knowledge / agents) benefit from more room
const wideTabs: Tab[] = ["rag", "agents"];

export function Settings() {
  const { t } = useTranslation();
  const [tab, setTab] = useState<Tab>("prompt");
  const [navOpen, setNavOpen] = useState(true);

  const navRow = (n: NavItem) => {
    const active = tab === n.id;
    return (
      <div
        key={n.id}
        onClick={() => setTab(n.id)}
        style={{ display: "flex", alignItems: "center", gap: 9, padding: "8px 9px", borderRadius: 7, cursor: "pointer", background: active ? "var(--tint-active)" : "transparent", border: `1px solid ${active ? "var(--tint-active-bd)" : "transparent"}` }}
      >
        <div style={{ width: 7, height: 7, borderRadius: "50%", background: active ? n.dot : "var(--bd-sep)", flex: "none" }} />
        <span style={{ font: "500 12px 'IBM Plex Sans'", color: active ? "var(--tx)" : "var(--tx2)" }}>{t(`settings.nav.${n.id}`)}</span>
      </div>
    );
  };

  return (
    <div style={{ flex: 1, display: "flex", minHeight: 0, minWidth: 0, position: "relative" }}>
      {/* nav */}
      {navOpen ? (
        <div style={{ width: 224, flex: "none", background: "var(--bg-panel)", borderRight: "1px solid var(--bd)", padding: "16px 12px", display: "flex", flexDirection: "column", gap: 3, overflowY: "auto" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "0 4px 9px" }}>
            <span style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.6px", flex: 1 }}>OPTIMIZATION</span>
            <div onClick={() => setNavOpen(false)} title={t("settings.collapseSidebar")} style={{ cursor: "pointer", width: 22, height: 22, borderRadius: 6, border: "1px solid var(--bd2)", background: "var(--bg-card2)", display: "flex", alignItems: "center", justifyContent: "center", color: "var(--tx3)" }}>
              <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6"><path d="M10 4L6 8l4 4" /></svg>
            </div>
          </div>
          {optimization.map(navRow)}
          <div style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.6px", padding: "16px 8px 9px" }}>SECURITY</div>
          {security.map(navRow)}
          <div style={{ marginTop: "auto", display: "flex", alignItems: "center", gap: 7, padding: 9, background: "var(--tint-green)", border: "1px solid var(--tint-green-bd)", borderRadius: 8 }}>
            <div style={{ width: 6, height: 6, borderRadius: "50%", background: "#3fbf8f", boxShadow: "0 0 6px #3fbf8f" }} />
            <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "#67c9a4" }}>{t("settings.allPoliciesActive")}</span>
          </div>
        </div>
      ) : (
        <div style={{ width: 40, flex: "none", background: "var(--bg-panel)", borderRight: "1px solid var(--bd)", padding: "14px 0", display: "flex", flexDirection: "column", alignItems: "center", gap: 10 }}>
          <div onClick={() => setNavOpen(true)} title={t("settings.expandSidebar")} style={{ cursor: "pointer", width: 24, height: 24, borderRadius: 6, border: "1px solid var(--bd2)", background: "var(--bg-card2)", display: "flex", alignItems: "center", justifyContent: "center", color: "var(--tx3)" }}>
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6"><path d="M6 4l4 4-4 4" /></svg>
          </div>
          <div style={{ writingMode: "vertical-rl", font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "1px", marginTop: 4 }}>SETTINGS</div>
        </div>
      )}

      {/* body */}
      <div style={{ flex: 1, minWidth: 0, overflowY: "auto", background: "var(--bg-app)", padding: "26px 32px" }}>
        <div style={{ maxWidth: wideTabs.includes(tab) ? 1080 : 760, minWidth: 0, display: "flex", flexDirection: "column", gap: 22 }}>
          {tab === "prompt" && <PromptPanel />}
          {tab === "history" && <HistoryPanel />}
          {tab === "rag" && <RagPanel />}
          {tab === "agents" && <AgentsPanel />}
          {tab === "tools" && <ToolsPanel />}
          {tab === "sources" && <SourcesPanel />}
          {tab === "repos" && <RepositoriesPanel />}
          {tab === "sandbox" && <SandboxPanel />}
          {tab === "proxy" && <ProxyPanel />}
        </div>
      </div>
    </div>
  );
}
