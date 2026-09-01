import type { RunLabel } from "@/lib/runLabels";
import type { StageState } from "@/lib/sandbox";
import { Trans, useTranslation } from "react-i18next";
import type { Trace } from "./trace";
import { Button } from "./ui";

// The trace banner and its stage stepper.
//
// Wording is deliberate throughout. What the gateway recorded is what came back
// to a stage, not what the model read, so this says "reached", never "read" —
// the screen is used to decide a group was unnecessary, and only the negative
// direction is provable. A stage that reached nothing is evidence; a stage that
// reached ten documents is not evidence it needed them.

/** Date and time of a trace, always with the date — see Trace.time. */
function traceWhen(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function TraceBar({
  trace,
  stages,
  label,
  selected,
  onSelect,
  onClear,
  onOpenPrompt,
  drawerOpen,
}: {
  trace: Trace;
  /** the run's declared stages, so ones that retrieved nothing still appear. */
  stages: StageState[];
  /** what the run was, when a history names it. */
  label: RunLabel | null;
  selected: string | null;
  onSelect: (stageId: string | null) => void;
  onClear: () => void;
  onOpenPrompt: () => void;
  drawerOpen: boolean;
}) {
  const { t } = useTranslation();
  // Declared stages first, in run order, then anything the log shows that the
  // run status did not list (a delegated sub-agent, say).
  const declared = stages.map((s) => ({ id: s.id, name: s.name || s.id, role: s.role }));
  const extra = trace.stages
    .filter((t) => !declared.some((d) => d.id === t.id))
    .map((t) => ({ id: t.id, name: t.id, role: "" }));
  const all = [...declared, ...extra];

  const reachedOf = (id: string) => trace.stages.find((s) => s.id === id)?.reached.size ?? 0;
  const queriesOf = (id: string) => trace.stages.find((s) => s.id === id)?.queries.length ?? 0;
  const cutOf = (id: string) => trace.stages.find((s) => s.id === id)?.truncated ?? false;

  return (
    <div
      style={{
        flex: "none",
        background: "var(--tint-active)",
        borderBottom: "1px solid var(--tint-active-bd)",
        display: "flex",
        flexDirection: "column",
      }}
    >
      <div style={{ height: 34, display: "flex", alignItems: "center", gap: 9, padding: "0 14px" }}>
        <div
          style={{
            width: 6,
            height: 6,
            borderRadius: "50%",
            background: "var(--cyan)",
            boxShadow: "0 0 7px var(--cyan)",
            flex: "none",
          }}
        />
        {/* Named by the work when a history knows it, by the id otherwise. The
            id is kept either way — it is what a log search takes, and losing it
            to make room for a title would trade one missing half for the
            other. */}
        <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx2)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", minWidth: 0 }}>
          {label ? (
            <Trans i18nKey="knowledge.traceOfTask" values={{ task: label.title }} components={{ task: <span style={{ color: "var(--cyan)" }} /> }} />
          ) : (
            <Trans i18nKey="knowledge.traceOfRun" values={{ run: trace.run }} components={{ run: <span style={{ fontFamily: "'IBM Plex Mono'", color: "var(--cyan)" }} /> }} />
          )}
        </span>
        {/* The date is not decoration. A trace opened from the audit list can be
            weeks old, and the whole point of reading one is to judge a
            particular run — so it says which. */}
        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", whiteSpace: "nowrap", flex: "none" }} title={label?.sub}>
          {[traceWhen(trace.time), label ? `${label.kind}${label.sub ? ` · ${label.sub}` : ""}` : "", label ? trace.run : ""]
            .filter(Boolean)
            .join(" · ")}
        </span>
        <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)", whiteSpace: "nowrap" }}>
          {t("knowledge.searchesReached", { queries: trace.queryCount, nodes: trace.reached.size })}
        </span>
        {trace.truncated ? (
          <span
            style={{
              font: "600 9px 'IBM Plex Mono'",
              color: "var(--amber)",
              background: "var(--tint-amber)",
              border: "1px solid #43331c",
              padding: "2px 7px",
              borderRadius: 4,
              whiteSpace: "nowrap",
            }}
            title={t("knowledge.partialRecordTip")}
          >
            {t("knowledge.partialRecord")}
          </span>
        ) : null}
        <div style={{ flex: 1 }} />
        <Button tone="accent" onClick={onOpenPrompt}>
          {t(drawerOpen ? "knowledge.closePrompt" : "knowledge.editPrompt")}
        </Button>
        <Button onClick={onClear}>{t("knowledge.clearTrace")}</Button>
      </div>
      <div
        style={{
          minHeight: 42,
          display: "flex",
          alignItems: "center",
          gap: 7,
          padding: "0 14px 6px",
          overflowX: "auto",
        }}
      >
        <div
          onClick={() => onSelect(null)}
          style={stagePill(selected === null, "var(--cyan)")}
        >
          {t("knowledge.allStages")}
        </div>
        <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>|</span>
        {all.map((s, i) => {
          const n = reachedOf(s.id);
          const on = selected === s.id;
          return (
            <div key={s.id} style={{ display: "flex", alignItems: "center", gap: 7 }}>
              <div onClick={() => onSelect(on ? null : s.id)} style={stagePill(on, "var(--ac)")}>
                <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                  {i + 1}
                </span>
                <span style={{ font: "600 10.5px 'IBM Plex Sans'" }}>{s.name}</span>
                {s.role ? (
                  <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                    {s.role}
                  </span>
                ) : null}
                <span
                  style={{
                    font: "600 9.5px 'IBM Plex Mono'",
                    color: n ? "var(--cyan)" : "var(--tx-faint)",
                  }}
                  title={t("knowledge.searchesReached", { queries: queriesOf(s.id), nodes: n })}
                >
                  {n}
                </span>
                {cutOf(s.id) ? (
                  <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--amber)" }}>!</span>
                ) : null}
              </div>
              {i < all.length - 1 ? (
                <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>→</span>
              ) : null}
            </div>
          );
        })}
        <div style={{ flex: 1 }} />
        <span
          style={{
            font: "400 9.5px 'IBM Plex Mono'",
            color: "var(--tx-faint)",
            flex: "none",
            whiteSpace: "nowrap",
            paddingLeft: 10,
          }}
        >
          {t("knowledge.returnedNotRead")}
        </span>
      </div>
    </div>
  );
}

function stagePill(active: boolean, color: string) {
  return {
    display: "flex",
    alignItems: "center",
    gap: 6,
    padding: "4px 10px",
    borderRadius: 6,
    cursor: "pointer",
    userSelect: "none" as const,
    whiteSpace: "nowrap" as const,
    flex: "none" as const,
    color: active ? color : "var(--tx3)",
    background: active ? "var(--bg-deep)" : "transparent",
    border: `1px solid ${active ? color : "var(--bd2)"}`,
  };
}
