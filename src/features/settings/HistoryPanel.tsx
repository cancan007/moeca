import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cardStyle, sectionTitle, SegGroup, Slider, Toggle } from "./ui";

const strategyIds = ["sum", "full", "recent"];

export function HistoryPanel() {
  const { t } = useTranslation();
  const [on, setOn] = useState(true);
  const [strat, setStrat] = useState("sum");
  const [threshold, setThreshold] = useState(70);

  const strategies = strategyIds.map((id) => ({ id, title: t(`settings.history.strategies.${id}.title`), sub: t(`settings.history.strategies.${id}.sub`) }));

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 22 }}>
      {sectionTitle(t("settings.nav.history"), t("settings.history.desc"))}

      <div style={{ ...cardStyle, gap: 16 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.history.enable")}</span>
          <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "#3fbf8f", background: "var(--tint-green)", padding: "2px 7px", borderRadius: 5 }}>−38% tok</span>
          <Toggle on={on} onClick={() => setOn(!on)} marginLeft="auto" />
        </div>

        <SegGroup items={strategies} value={strat} onChange={setStrat} />

        <div style={{ display: "flex", alignItems: "center", gap: 12, paddingTop: 4 }}>
          <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx3)", width: 140 }}>{t("settings.history.threshold")}</span>
          <Slider value={threshold} min={30} max={95} onChange={setThreshold} />
          <span style={{ font: "500 11px 'IBM Plex Mono'", color: "#34d3e0", width: 74, textAlign: "right" }}>{threshold}% ctx</span>
        </div>
      </div>
    </div>
  );
}
