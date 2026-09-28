import { useTranslation } from "react-i18next";
import { useStore } from "@/store/useStore";
import { AUTO_MARGIN, type CompactStrategy } from "@/lib/chatContext";
import { cardStyle, sectionTitle, SegGroup, Slider, Toggle } from "./ui";

const strategyIds: CompactStrategy[] = ["sum", "full", "recent"];

/**
 * History compaction.
 *
 * This panel used to hold its three values in component state, which meant the
 * toggle, the strategy and the threshold were drawn, moved, and then discarded —
 * nothing downstream ever read them. They live in the store now and reach two
 * places:
 *
 *   - Chat reduces a conversation's transcript with them between turns.
 *   - Every compiled stage carries a context budget derived from them, so the
 *     in-container compaction the agent runtime has always done finally follows
 *     what the agent was configured with instead of its built-in 120k.
 *
 * The compactor is configuration rather than a stage. Summarizing is one model
 * call with no tools and no thinking — running it as a container would buy an
 * orchestrated stage, a working directory and a handoff manifest for a single
 * request — so the Solo chosen here supplies the model and the system prompt, and
 * the host makes the call through the gateway.
 */
export function HistoryPanel() {
  const { t } = useTranslation();
  const history = useStore((s) => s.history);
  const setHistory = useStore((s) => s.setHistory);
  const compactorSoloId = useStore((s) => s.compactorSoloId);
  const setCompactorSoloId = useStore((s) => s.setCompactorSoloId);
  const lastCompaction = useStore((s) => s.lastCompaction);
  const solos = useStore((s) => s.solos);
  const providers = useStore((s) => s.providers);

  const strategies = strategyIds.map((id) => ({
    id,
    title: t(`settings.history.strategies.${id}.title`),
    sub: t(`settings.history.strategies.${id}.sub`),
  }));

  // Only Anthropic-dialect agents are offered: the host speaks that one shape
  // when it asks for a summary, and listing an agent whose call it would get
  // subtly wrong is worse than a shorter list.
  const anthropic = new Set(
    providers.filter((p) => (p.dialect || "anthropic") === "anthropic").map((p) => p.name),
  );
  const candidates = solos.filter((s) => s.kind !== "command" && anthropic.has(s.providerId));

  const saving = lastCompaction && lastCompaction.replaced > 0
    ? Math.round(((lastCompaction.replaced - lastCompaction.summary) / lastCompaction.replaced) * 100)
    : null;

  const disabled = !history.on || history.strategy === "full";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 22 }}>
      {sectionTitle(t("settings.nav.history"), t("settings.history.desc"))}

      <div style={{ ...cardStyle, gap: 16 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.history.enable")}</span>
          <span
            style={{
              font: "500 9.5px 'IBM Plex Mono'",
              color: saving == null ? "var(--tx-faint)" : "#3fbf8f",
              background: saving == null ? "transparent" : "var(--tint-green)",
              padding: "2px 7px", borderRadius: 5,
            }}
          >
            {saving == null ? t("settings.history.savingNone") : t("settings.history.saving", { pct: saving })}
          </span>
          <Toggle on={history.on} onClick={() => setHistory({ on: !history.on })} marginLeft="auto" />
        </div>

        <SegGroup items={strategies} value={history.strategy} onChange={(id) => setHistory({ strategy: id as CompactStrategy })} />

        <div style={{ display: "flex", alignItems: "center", gap: 12, paddingTop: 4, opacity: disabled ? 0.45 : 1 }}>
          <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx3)", width: 140 }}>{t("settings.history.threshold")}</span>
          <Slider value={history.thresholdPct} min={30} max={95} onChange={(v) => setHistory({ thresholdPct: v })} />
          <span style={{ font: "500 11px 'IBM Plex Mono'", color: "#34d3e0", width: 74, textAlign: "right" }}>{history.thresholdPct}% ctx</span>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 12, opacity: disabled ? 0.45 : 1 }}>
          <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx3)", width: 140 }}>{t("settings.history.keepTurns")}</span>
          <Slider value={history.keepTurns} min={1} max={12} onChange={(v) => setHistory({ keepTurns: v })} />
          <span style={{ font: "500 11px 'IBM Plex Mono'", color: "#34d3e0", width: 74, textAlign: "right" }}>
            {t("settings.history.keepTurnsUnit", { count: history.keepTurns })}
          </span>
        </div>

        {/* Past this point a conversation compacts without asking: the threshold
            is where it offers, and there has to be room left to offer IN. */}
        <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)", lineHeight: 1.6 }}>
          {Math.min(history.thresholdPct + AUTO_MARGIN, 98)}% → auto
        </span>
      </div>

      <div style={{ ...cardStyle, gap: 12, opacity: history.strategy === "sum" && history.on ? 1 : 0.55 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.history.compactor")}</span>
        </div>
        <select
          value={compactorSoloId}
          onChange={(e) => setCompactorSoloId(e.target.value)}
          style={{ background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "9px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none", colorScheme: "dark" }}
        >
          <option value="">{t("settings.history.compactorAuto")}</option>
          {candidates.map((s) => (
            <option key={s.id} value={s.id}>{s.name} — {s.model}</option>
          ))}
        </select>
        <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.7 }}>
          {t("settings.history.compactorHint")}
        </span>
        <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)", lineHeight: 1.6 }}>
          {t("settings.history.compactorDialectNote")}
        </span>
      </div>

      <div style={{ ...cardStyle, gap: 8 }}>
        <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.history.layers")}</span>
        <span style={{ font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.75 }}>
          {t("settings.history.layersBody")}
        </span>
      </div>
    </div>
  );
}
