import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cardStyle, sectionTitle, SegGroup, Toggle } from "./ui";

const levelIds = ["light", "std", "agg"];
const rowKeys = ["prompt", "tool"];

export function PromptPanel() {
  const { t } = useTranslation();
  const [level, setLevel] = useState("std");
  const [toggles, setToggles] = useState<Record<string, boolean>>({ prompt: true, tool: true });

  const levels = levelIds.map((id) => ({ id, title: t(`settings.prompt.levels.${id}.title`), sub: t(`settings.prompt.levels.${id}.sub`) }));
  const rows = rowKeys.map((key) => ({ key, title: t(`settings.prompt.rows.${key}.title`), sub: t(`settings.prompt.rows.${key}.sub`) }));

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 22 }}>
      {sectionTitle(t("settings.nav.prompt"), t("settings.prompt.desc"))}

      <div style={cardStyle}>
        <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.prompt.levelLabel")}</span>
        <SegGroup items={levels} value={level} onChange={setLevel} />
      </div>

      <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "6px 20px" }}>
        {rows.map((r, i) => (
          <div key={r.key} style={{ display: "flex", alignItems: "center", gap: 12, padding: "15px 0", borderBottom: i < rows.length - 1 ? "1px solid var(--bd-soft)" : "none" }}>
            <div style={{ display: "flex", flexDirection: "column", gap: 3, flex: 1 }}>
              <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{r.title}</span>
              <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{r.sub}</span>
            </div>
            <Toggle on={toggles[r.key]} onClick={() => setToggles((t) => ({ ...t, [r.key]: !t[r.key] }))} />
          </div>
        ))}
      </div>
    </div>
  );
}
