import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { rag } from "@/lib/rag";
import { useStore } from "@/store/useStore";
import { AssignMode } from "./AssignMode";
import { NodeMode } from "./NodeMode";
import { MeaningMode } from "./MeaningMode";
import { PromptDrawer } from "./PromptDrawer";
import { TraceBar } from "./TraceBar";
import { Notice, pillStyle } from "./ui";
import { useKnowledge } from "./useKnowledge";
import { useTrace } from "./useTrace";

// The Knowledge screen.
//
// Three views over one body of data:
//
//   Node view     what the embeddings say — where documents sit, what is near what
//   Meaning view  what a person declared — groups as regions, relations between them
//   Assign view   the hierarchy, and which sources each group may retrieve
//
// The middle view is named for meaning rather than for range: "range" collided
// with the retrieval scope the assign view actually sets, and it
// covered only half of what is edited here — a relation is not a classification,
// it is a meaning someone asserted between two things.
//
// The first two share the same node positions on purpose. Laying the declared
// view out independently would make the two incomparable, and comparing them —
// does the structure I drew match the structure the content has? — is the
// reason for having both.

type Mode = "node" | "meaning" | "assign";

const MODES: Mode[] = ["node", "meaning", "assign"];



export function Knowledge() {
  const { t } = useTranslation();
  const [mode, setMode] = useState<Mode>("node");
  const [reindexing, setReindexing] = useState(false);
  const [stageFilter, setStageFilter] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const { graph, index, graphError, indexError, loading, reload, reloadGraph } = useKnowledge();
  const { runId, trace, stages, error: traceError, setRun } = useTrace();
  const solos = useStore((s) => s.solos);
  const staticTpls = useStore((s) => s.staticTpls);
  const upsertSolo = useStore((s) => s.upsertSolo);

  // Which sources the current stage selection reached. Undefined means no
  // trace at all, which the canvas renders normally; an empty set means a
  // trace that reached nothing, which it must dim rather than ignore.
  const reached = useMemo(() => {
    if (!runId) return undefined;
    if (stageFilter === null) return new Set(trace.reached.keys());
    return new Set(trace.stages.find((s) => s.id === stageFilter)?.reached.keys() ?? []);
  }, [runId, stageFilter, trace]);

  // A stage id is not an agent id, so the agent is found through the templates
  // that name it. Failing to find one is shown rather than guessed — the
  // template may have been edited or deleted since the run.
  const stageAgent = useMemo(() => {
    const id = stageFilter ?? stages[0]?.id;
    if (!id) return undefined;
    const stage = stages.find((s) => s.id === id);
    for (const t of staticTpls) {
      if (t.pattern !== "graph") continue;
      const node = t.nodes.find((n) => n.id === id || n.soloId === stage?.role);
      if (node?.soloId) return solos.find((x) => x.id === node.soloId);
    }
    // A one-stage run is a solo template, where the stage carries the role.
    return solos.find((x) => x.id === stage?.role || x.name === stage?.name);
  }, [stageFilter, stages, staticTpls, solos]);

  const usedBy = useMemo(
    () =>
      stageAgent
        ? staticTpls.filter(
            (t) => t.pattern === "graph" && t.nodes.some((n) => n.soloId === stageAgent.id),
          ).length + 1
        : 0,
    [stageAgent, staticTpls],
  );

  const reindex = async () => {
    setReindexing(true);
    try {
      await rag.reindex();
      // The rebuild is asynchronous on the indexer's side, so this reload sees
      // the state at the moment the request landed. Reloading again once it
      // settles is the user's call — an automatic poll here would fight with
      // whatever they are editing.
      reload();
    } finally {
      setReindexing(false);
    }
  };

  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
        minWidth: 0,
      }}
    >
      <div
        style={{
          height: 44,
          flex: "none",
          borderBottom: "1px solid var(--bd)",
          background: "var(--bg-panel)",
          display: "flex",
          alignItems: "center",
          gap: 10,
          padding: "0 14px",
        }}
      >
        <span
          style={{
            font: "700 12.5px 'IBM Plex Sans'",
            color: "var(--tx)",
            letterSpacing: "-.1px",
            flex: "none",
          }}
        >
          {t("knowledge.title")}
        </span>
        <div
          style={{
            display: "flex",
            flex: "none",
            background: "var(--bg-deep)",
            border: "1px solid var(--bd2)",
            borderRadius: 7,
            padding: 2,
            gap: 2,
          }}
        >
          {MODES.map((m) => (
            <div key={m} onClick={() => setMode(m)} style={pillStyle(mode === m)}>
              {t(`knowledge.mode.${m}`)}
            </div>
          ))}
        </div>
        <span
          style={{
            font: "400 10px 'IBM Plex Mono'",
            color: "var(--tx-faint)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {t(`knowledge.modeHint.${mode}`)}
        </span>
        <div style={{ flex: 1 }} />
        <span
          style={{
            font: "500 10px 'IBM Plex Mono'",
            color: "var(--tx-faint)",
            flex: "none",
            whiteSpace: "nowrap",
          }}
        >
          {loading
            ? t("common.loading")
            : reindexing
              ? t("knowledge.reindexing")
              : `${index.nodes.length} nodes · ${graph.groups.length} groups · ${graph.relations.length} relations`}
        </span>
      </div>

      {graphError ? (
        <div style={{ padding: "14px 16px" }}>
          <Notice tone="error">
            {t("knowledge.loadFailed")}
            <br />
            <span style={{ font: "400 9.5px 'IBM Plex Mono'" }}>{graphError}</span>
          </Notice>
        </div>
      ) : null}

      {runId ? (
        <TraceBar
          trace={trace}
          stages={stages}
          selected={stageFilter}
          onSelect={setStageFilter}
          onClear={() => {
            setRun(null);
            setStageFilter(null);
            setDrawerOpen(false);
          }}
          onOpenPrompt={() => setDrawerOpen(!drawerOpen)}
          drawerOpen={drawerOpen}
        />
      ) : null}

      {traceError ? (
        <div style={{ padding: "10px 16px" }}>
          <Notice tone="warn">{traceError}</Notice>
        </div>
      ) : null}

      {mode === "node" ? (
        <NodeMode
          graph={graph}
          index={index}
          indexError={indexError}
          onReindex={reindex}
          reached={reached}
          drawer={
            drawerOpen ? (
              <PromptDrawer
                agent={stageAgent}
                stageName={
                  stages.find((s) => s.id === (stageFilter ?? stages[0]?.id))?.name ??
                  stageFilter ??
                  t("knowledge.stage")
                }
                usedByTasks={usedBy}
                onSave={(system) => {
                  if (stageAgent) upsertSolo({ ...stageAgent, system });
                  setDrawerOpen(false);
                }}
                onClose={() => setDrawerOpen(false)}
              />
            ) : null
          }
        />
      ) : mode === "meaning" ? (
        <MeaningMode graph={graph} index={index} onChanged={reloadGraph} />
      ) : (
        <AssignMode graph={graph} index={index} onChanged={reloadGraph} />
      )}
    </div>
  );
}
