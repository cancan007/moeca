import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { knowledge, shortLabel, type GraphNode, type IndexGraph, type KnowledgeGraph } from "@/lib/knowledge";
import { rag } from "@/lib/rag";
import type { ReceivedPassage } from "./trace";
import { selectEdges, strokeWidth, totalPairs } from "./edges";
import { hullPath, type Point } from "./geometry";
import {
  Button,
  EmptyState,
  Notice,
  Section,
  Swatch,
  monoMeta,
  panel,
  railHeading,
  selectableRow,
} from "./ui";

// Mode 1 — the similarity view.
//
// Everything drawn here is computed from the embeddings: where a node sits is a
// projection of its content, and an edge means two documents are actually close
// in that space. Nothing on this canvas is authored, which is the point — it is
// the machine's view of the index, to be compared against the regions the user
// declared in mode 2.

const W = 700;
const H = 520;
const PAD = 34;

export function NodeMode({
  graph,
  index,
  indexError,
  onReindex,
  onChanged,
  reached,
  passagesOf,
  drawer,
}: {
  graph: KnowledgeGraph;
  index: IndexGraph;
  indexError: string;
  onReindex: () => void;
  /** re-read the authored graph after an assignment. Absent leaves the
   *  inspector read-only, which is what a screen with no way to save should be
   *  rather than one whose save does nothing. */
  onChanged?: () => Promise<void>;
  /** sources a trace reached. undefined = no trace; empty = reached nothing,
   *  which must still dim the canvas rather than read as "no trace". */
  reached?: Set<string>;
  /** what the run actually received from a given source. Separate from
   *  `reached` because reaching is a fact about the log and the passages are
   *  what was in it — a source can be reached with nothing readable captured. */
  passagesOf?: (source: string) => ReceivedPassage[];
  drawer?: ReactNode;
}) {
  const { t } = useTranslation();
  const [topN, setTopN] = useState(3);
  const [threshold, setThreshold] = useState(0.72);
  const [mutual, setMutual] = useState(true);
  const [showLocal, setShowLocal] = useState(true);
  const [showExternal, setShowExternal] = useState(true);
  const [groupFilter, setGroupFilter] = useState<string | null>(null);
  const [selected, setSelected] = useState<number | null>(null);

  const nodes = index.nodes;

  // Membership comes from the authored graph rather than from whatever the
  // indexer was last configured with, so a group edited a moment ago is
  // reflected here without waiting for a reindex.
  const groupsBySource = useMemo(() => {
    const m = new Map<string, typeof graph.groups>();
    for (const g of graph.groups) {
      for (const s of g.sources) {
        const list = m.get(s) ?? [];
        list.push(g);
        m.set(s, list);
      }
    }
    return m;
  }, [graph.groups]);

  const groupsOf = (n: GraphNode) => groupsBySource.get(n.source) ?? [];

  const visible = useMemo(() => {
    const inGroup = (n: GraphNode) =>
      groupFilter === null || groupsOf(n).some((g) => g.id === groupFilter);
    return (i: number) => {
      const n = nodes[i];
      if (!n) return false;
      if (n.kind === "external" ? !showExternal : !showLocal) return false;
      return inGroup(n);
    };
    // groupsOf closes over groupsBySource, which is already a dependency.
  }, [nodes, showLocal, showExternal, groupFilter, groupsBySource]);

  const edges = useMemo(
    () => selectEdges(nodes, { topN, threshold, mutual, visible }),
    [nodes, topN, threshold, mutual, visible],
  );

  const localN = nodes.filter((n) => n.kind !== "external").length;
  const extN = nodes.length - localN;
  const pairs = totalPairs(nodes, visible);

  // 0..1 from the indexer, padded so nodes never touch the frame.
  const px = (n: GraphNode) => PAD + n.x * (W - PAD * 2);
  const py = (n: GraphNode) => PAD + n.y * (H - PAD * 2);

  const hull = useMemo(() => {
    if (groupFilter === null) return null;
    const g = graph.groups.find((x) => x.id === groupFilter);
    if (!g) return null;
    const pts: Point[] = nodes
      .map((n, i) => ({ n, i }))
      .filter(({ n, i }) => visible(i) && g.sources.includes(n.source))
      .map(({ n }) => [px(n), py(n)] as Point);
    if (!pts.length) return null;
    return { d: hullPath(pts, 18), color: g.color || "var(--ac)" };
  }, [groupFilter, graph.groups, nodes, visible]);

  const sel = selected !== null ? nodes[selected] : undefined;

  return (
    <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
      <div style={{ ...panel, width: 212, flex: "none", borderRight: "1px solid var(--bd)" }}>
        <div
          style={{
            padding: "12px 13px 11px",
            display: "flex",
            flexDirection: "column",
            gap: 8,
            borderBottom: "1px solid var(--bd-soft)",
          }}
        >
          <span style={railHeading}>NODE TYPE</span>
          <div onClick={() => setShowLocal(!showLocal)} style={selectableRow(showLocal)}>
            <div style={{ width: 8, height: 8, borderRadius: "50%", background: "var(--ac)" }} />
            <span style={{ font: "500 11px 'IBM Plex Sans'", flex: 1, color: "var(--tx3)" }}>
              local file
            </span>
            <span style={monoMeta}>{localN}</span>
          </div>
          <div onClick={() => setShowExternal(!showExternal)} style={selectableRow(showExternal)}>
            <div
              style={{
                width: 8,
                height: 8,
                borderRadius: "50%",
                border: "1.6px solid var(--purple)",
              }}
            />
            <span style={{ font: "500 11px 'IBM Plex Sans'", flex: 1, color: "var(--tx3)" }}>
              external https
            </span>
            <span style={monoMeta}>{extN}</span>
          </div>
        </div>

        <div
          style={{
            padding: "12px 13px 13px",
            display: "flex",
            flexDirection: "column",
            gap: 11,
            borderBottom: "1px solid var(--bd-soft)",
          }}
        >
          <span style={railHeading}>SIMILARITY EDGES</span>
          <Slider
            label={t("knowledge.node.topPerNode")}
            value={topN}
            display={t("daily.countUnit", { count: topN })}
            min={1}
            max={6}
            step={1}
            onChange={setTopN}
          />
          <Slider
            label={t("knowledge.node.cosAtLeast")}
            value={threshold}
            display={threshold.toFixed(2)}
            min={0.4}
            max={0.98}
            step={0.01}
            onChange={setThreshold}
          />
          <div onClick={() => setMutual(!mutual)} style={selectableRow(false)}>
            <div
              style={{
                width: 13,
                height: 13,
                borderRadius: 3.5,
                border: `1px solid ${mutual ? "var(--ac)" : "var(--bd2)"}`,
                background: mutual ? "var(--ac)" : "transparent",
                color: "var(--bg-deep)",
                font: "700 9px 'IBM Plex Sans'",
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
              }}
            >
              {mutual ? "✓" : ""}
            </div>
            <span style={{ font: "500 10.5px 'IBM Plex Sans'", flex: 1, color: "var(--tx3)" }}>
              {t("knowledge.node.mutualKnnOnly")}
            </span>
          </div>
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 4,
              background: "var(--bg-deep)",
              border: "1px solid var(--bd2)",
              borderRadius: 7,
              padding: "8px 9px",
            }}
          >
            <Stat label={t("knowledge.node.drawn")} value={String(edges.length)} strong />
            <Stat label={t("knowledge.node.totalPairs")} value={String(pairs)} />
            <div
              style={{
                height: 3,
                borderRadius: 2,
                background: "var(--bd2)",
                overflow: "hidden",
                marginTop: 2,
              }}
            >
              <div
                style={{
                  height: "100%",
                  width: `${pairs ? Math.min(100, (edges.length / pairs) * 100) : 0}%`,
                  background: "var(--ac)",
                }}
              />
            </div>
          </div>
        </div>

        <div style={{ padding: "12px 13px", display: "flex", flexDirection: "column", gap: 6 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 7, marginBottom: 2 }}>
            <span style={railHeading}>GROUPS</span>
            <div style={{ flex: 1 }} />
            <span style={monoMeta}>{graph.groups.length}</span>
          </div>
          <div
            onClick={() => setGroupFilter(null)}
            style={selectableRow(groupFilter === null)}
          >
            <span style={{ font: "500 11px 'IBM Plex Sans'", flex: 1, color: "var(--tx3)" }}>
              {t("knowledge.node.all")}
            </span>
          </div>
          {graph.groups.map((g) => (
            <div
              key={g.id}
              onClick={() => setGroupFilter(groupFilter === g.id ? null : g.id)}
              style={selectableRow(groupFilter === g.id)}
            >
              <Swatch color={g.color} />
              <span
                style={{
                  font: "500 11px 'IBM Plex Sans'",
                  flex: 1,
                  minWidth: 0,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  color: "var(--tx3)",
                }}
              >
                {g.name}
              </span>
              <span style={monoMeta}>{g.sources.length}</span>
            </div>
          ))}
        </div>
      </div>

      <div
        style={{
          flex: 1,
          position: "relative",
          minWidth: 0,
          background: "var(--bg-app)",
          overflow: "hidden",
        }}
      >
        {drawer}
        {indexError ? (
          <div style={{ padding: 22, maxWidth: 520 }}>
            <Notice tone="error">
              {t("knowledge.node.indexLoadFailed")}
              <br />
              <span style={{ font: "400 9.5px 'IBM Plex Mono'" }}>{indexError}</span>
            </Notice>
          </div>
        ) : nodes.length === 0 ? (
          <EmptyState
            title={t("knowledge.node.emptyIndex")}
            hint={t("knowledge.node.emptyIndexHint")}
          />
        ) : (
          <>
            {index.degenerate ? (
              <div style={{ position: "absolute", left: 16, top: 14, right: 16, zIndex: 5 }}>
                <Notice tone="warn">
                  {t("knowledge.node.degenerateProjection")}
                </Notice>
              </div>
            ) : null}
            <div
              style={{
                position: "absolute",
                left: "50%",
                top: "50%",
                width: W,
                height: H,
                margin: `-${H / 2}px 0 0 -${W / 2}px`,
              }}
            >
              <svg viewBox={`0 0 ${W} ${H}`} style={{ width: W, height: H }}>
                {hull ? (
                  <path
                    d={hull.d}
                    fill={hull.color}
                    fillOpacity={0.05}
                    stroke={hull.color}
                    strokeOpacity={0.5}
                    strokeWidth={1}
                    strokeDasharray="4 4"
                  />
                ) : null}
                {edges.map((e) => {
                  const a = nodes[e.a];
                  const b = nodes[e.b];
                  const lit = selected === e.a || selected === e.b;
                  return (
                    <line
                      key={`${e.a}-${e.b}`}
                      x1={px(a)}
                      y1={py(a)}
                      x2={px(b)}
                      y2={py(b)}
                      stroke={lit ? "var(--ac)" : "var(--tx-faint)"}
                      strokeOpacity={lit ? 0.85 : reached ? 0.12 : 0.3}
                      strokeWidth={strokeWidth(e.score, threshold)}
                    />
                  );
                })}
                {nodes.map((n, i) => {
                  if (!visible(i)) return null;
                  const gs = groupsOf(n);
                  const color = gs.length ? gs[0].color || "var(--ac)" : "var(--tx-dim)";
                  const on = selected === i;
                  // Under a trace, a node the run never reached sinks rather
                  // than disappears: "this was here and went unused" is the
                  // finding, so it has to stay visible to be read.
                  const hit = !reached || reached.has(n.source);
                  return (
                    <g key={n.source}>
                      {hit && reached ? (
                        <circle
                          cx={px(n)}
                          cy={py(n)}
                          r={9}
                          fill="var(--cyan)"
                          opacity={0.18}
                        />
                      ) : null}
                      <circle
                        cx={px(n)}
                        cy={py(n)}
                        r={on ? 6.5 : 4.2}
                        fill={n.kind === "external" ? "transparent" : color}
                        fillOpacity={hit ? (on ? 1 : 0.85) : 0.18}
                        stroke={color}
                        strokeOpacity={hit ? (on ? 1 : 0.7) : 0.2}
                        strokeWidth={n.kind === "external" ? 1.6 : on ? 2 : 0}
                        style={{ cursor: "pointer" }}
                        onClick={() => setSelected(on ? null : i)}
                      />
                    </g>
                  );
                })}
              </svg>
              {sel ? (
                <div
                  style={{
                    position: "absolute",
                    left: px(sel),
                    top: py(sel) - 12,
                    transform: "translate(-50%,-100%)",
                    font: "600 10px 'IBM Plex Mono'",
                    color: "var(--tx)",
                    background: "var(--bg-deep)",
                    border: "1px solid var(--bd-sep)",
                    borderRadius: 4,
                    padding: "2px 7px",
                    whiteSpace: "nowrap",
                    pointerEvents: "none",
                  }}
                >
                  {shortLabel(sel.source)}
                </div>
              ) : null}
            </div>
          </>
        )}
      </div>

      <Inspector
        node={sel}
        nodes={nodes}
        groups={sel ? groupsOf(sel) : []}
        onPick={setSelected}
        onReindex={onReindex}
        traced={sel && reached ? reached.has(sel.source) : undefined}
        passages={sel && passagesOf ? passagesOf(sel.source) : undefined}
        allGroups={graph.groups}
        onChanged={onChanged}
      />
    </div>
  );
}

function Slider({
  label,
  value,
  display,
  min,
  max,
  step,
  onChange,
}: {
  label: string;
  value: number;
  display: string;
  min: number;
  max: number;
  step: number;
  onChange: (v: number) => void;
}) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
      <div style={{ display: "flex", alignItems: "baseline", gap: 6 }}>
        <span style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx3)", flex: 1 }}>
          {label}
        </span>
        <span style={{ font: "600 11px 'IBM Plex Mono'", color: "var(--ac)" }}>{display}</span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
        style={{ width: "100%", accentColor: "var(--ac)", height: 3 }}
      />
    </div>
  );
}

function Stat({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div style={{ display: "flex", alignItems: "baseline", gap: 6 }}>
      <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: 1 }}>
        {label}
      </span>
      <span
        style={{
          font: `${strong ? 600 : 500} ${strong ? 11 : 10}px 'IBM Plex Mono'`,
          color: strong ? "var(--tx2)" : "var(--tx-dim)",
        }}
      >
        {value}
      </span>
    </div>
  );
}

// The node inspector.
//
// It shows where a document lives and never lets it be edited here. What is on
// the canvas is a chunk boundary — a machine-chosen slice of about 1200
// characters that frequently cuts mid-sentence — so it is not a unit anyone
// should be editing. The path and the open action are the useful part; fix the
/** Choosing which groups a source belongs to.
 *
 *  A modal rather than checkboxes in the rail, because this is a decision made
 *  and then committed: membership decides what a scoped run may retrieve, and a
 *  rail that saved on every tick would apply half-made intentions. Nothing is
 *  written until 保存, and closing without it changes nothing.
 *
 *  The draft lives here rather than in the inspector so that reopening always
 *  starts from what is stored, not from what was abandoned last time. */
function GroupPicker({
  source,
  label,
  allGroups,
  onClose,
  onSave,
}: {
  source: string;
  label: string;
  allGroups: KnowledgeGraph["groups"];
  onClose: () => void;
  onSave: (picked: Set<string>) => Promise<void>;
}) {
  const { t } = useTranslation();
  const [picked, setPicked] = useState<Set<string>>(
    () => new Set(allGroups.filter((g) => g.sources.includes(source)).map((g) => g.id)),
  );
  const [saving, setSaving] = useState(false);

  const toggle = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  return (
    <div
      onClick={saving ? undefined : onClose}
      style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.62)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50, padding: 32 }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{ background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 12, width: "min(520px, 100%)", maxHeight: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}
      >
        <div style={{ padding: "13px 16px", borderBottom: "1px solid var(--bd-soft)", display: "flex", flexDirection: "column", gap: 3 }}>
          <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("knowledge.node.assignTitle")}</span>
          {/* Which source is being assigned. The rail behind is covered, and a
              picker that does not say what it is editing is one click from
              granting the wrong file. */}
          <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-faint)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={source}>
            {label}
          </span>
        </div>

        <div style={{ padding: "10px 12px", overflowY: "auto", display: "flex", flexDirection: "column", gap: 5 }}>
          {allGroups.map((g) => {
            const on = picked.has(g.id);
            return (
              <div
                key={g.id}
                onClick={saving ? undefined : () => toggle(g.id)}
                style={{
                  display: "flex", alignItems: "center", gap: 9, padding: "8px 10px", borderRadius: 8,
                  cursor: saving ? "default" : "pointer",
                  background: on ? "var(--tint-active)" : "var(--bg-card)",
                  border: `1px solid ${on ? "var(--tint-active-bd)" : "var(--bd2)"}`,
                }}
              >
                <div
                  style={{
                    width: 14, height: 14, borderRadius: 4, flex: "none",
                    border: `1px solid ${on ? g.color || "var(--ac)" : "var(--bd2)"}`,
                    background: on ? g.color || "var(--ac)" : "transparent",
                    color: "var(--bg-deep)", font: "700 9.5px 'IBM Plex Sans'",
                    display: "flex", alignItems: "center", justifyContent: "center",
                  }}
                >
                  {on ? "✓" : ""}
                </div>
                <Swatch color={g.color} />
                <span style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {g.name}
                </span>
                <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>
                  {t("knowledge.node.groupSourceCount", { count: g.sources.length })}
                </span>
              </div>
            );
          })}
        </div>

        <div style={{ padding: "11px 16px", borderTop: "1px solid var(--bd-soft)", display: "flex", alignItems: "center", gap: 10 }}>
          {/* What leaving it here would mean, said before the button that means
              it: a source in no group is everyone's, not nobody's. */}
          <span style={{ font: "400 9.5px 'IBM Plex Sans'", color: "var(--tx-dim)", flex: 1, lineHeight: 1.5 }}>
            {picked.size === 0 ? t("knowledge.node.assignNoneNote") : t("knowledge.node.assignSomeNote", { count: picked.size })}
          </span>
          <span onClick={saving ? undefined : onClose} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", cursor: saving ? "default" : "pointer", flex: "none" }}>
            {t("common.cancel")}
          </span>
          <div
            onClick={saving ? undefined : () => { setSaving(true); onSave(picked).finally(() => setSaving(false)); }}
            style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "7px 15px", borderRadius: 7, cursor: saving ? "default" : "pointer", flex: "none", opacity: saving ? 0.6 : 1 }}
          >
            {saving ? t("common.loading") : t("common.save")}
          </div>
        </div>
      </div>
    </div>
  );
}

/** A passage or a document, at readable size.
 *
 *  The inspector rail is 300px wide and a chunk is twelve hundred runes, so
 *  everything there is a preview. This is where the actual text is read, and it
 *  is a modal rather than a wider rail because reading is a thing you stop to
 *  do — the canvas behind it is not what you are looking at while you do it. */
function FullText({ title, body, onClose }: { title: string; body: string; onClose: () => void }) {
  const { t } = useTranslation();
  return (
    <div
      onClick={onClose}
      style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.62)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50, padding: 32 }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{ background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 12, width: "min(760px, 100%)", maxHeight: "100%", display: "flex", flexDirection: "column", overflow: "hidden" }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "12px 15px", borderBottom: "1px solid var(--bd-soft)" }}>
          <span style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {title}
          </span>
          <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>
            {t("knowledge.node.charCount", { count: body.length })}
          </span>
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-faint)", font: "400 13px 'IBM Plex Sans'", flex: "none" }}>✕</div>
        </div>
        {/* pre-wrap, not a paragraph: an indexed chunk keeps the line breaks it
            had in the file, and reflowing them would show the reader something
            other than what was embedded. */}
        <div style={{ padding: "14px 16px", overflowY: "auto", font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx2)", lineHeight: 1.75, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
          {body}
        </div>
      </div>
    </div>
  );
}

// file at its source and reindex.
function Inspector({
  node,
  nodes,
  groups,
  onPick,
  onReindex,
  traced,
  passages,
  allGroups,
  onChanged,
}: {
  node?: GraphNode;
  nodes: GraphNode[];
  groups: KnowledgeGraph["groups"];
  onPick: (i: number) => void;
  onReindex: () => void;
  /** undefined when no trace is active. */
  traced?: boolean;
  /** passages this run received from this source; undefined without a trace. */
  passages?: ReceivedPassage[];
  /** every group, so membership can be edited rather than only read. */
  allGroups: KnowledgeGraph["groups"];
  onChanged?: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  // The indexed text, read on demand rather than with the graph: it is the one
  // thing here big enough to be worth not loading for every node someone
  // clicks past.
  const [indexed, setIndexed] = useState<string | null>(null);
  const [reading, setReading] = useState(false);
  const [readErr, setReadErr] = useState("");
  const [full, setFull] = useState<{ title: string; body: string } | null>(null);
  const [picking, setPicking] = useState(false);
  const [assignErr, setAssignErr] = useState("");

  // A different node is a different document; anything loaded for the last one
  // would otherwise sit under the new one's name.
  useEffect(() => {
    setIndexed(null);
    setReadErr("");
    setFull(null);
    setPicking(false);
    setAssignErr("");
  }, [node?.source]);

  if (!node) {
    return (
      <div style={{ ...panel, width: 300, flex: "none", borderLeft: "1px solid var(--bd)" }}>
        <EmptyState
          title={t("knowledge.node.pickNode")}
          hint={t("knowledge.node.pickNodeHint")}
        />
      </div>
    );
  }

  const external = node.kind === "external";
  const target = external ? node.url ?? node.source : node.source;

  return (
    <div style={{ ...panel, width: 300, flex: "none", borderLeft: "1px solid var(--bd)" }}>
      <div
        style={{
          padding: "13px 14px 12px",
          borderBottom: "1px solid var(--bd-soft)",
          display: "flex",
          flexDirection: "column",
          gap: 8,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
          <Swatch color={external ? "var(--purple)" : "var(--ac)"} />
          <span style={railHeading}>{external ? "EXTERNAL HTTPS" : "LOCAL FILE"}</span>
          <div style={{ flex: 1 }} />
          {traced === undefined ? null : traced ? (
            <span
              style={{
                font: "600 9px 'IBM Plex Mono'",
                color: "var(--cyan)",
                background: "var(--tint-active)",
                border: "1px solid var(--tint-active-bd)",
                padding: "2px 6px",
                borderRadius: 4,
              }}
            >
              {t("knowledge.node.reachedInRun")}
            </span>
          ) : (
            <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
              {t("knowledge.node.notReached")}
            </span>
          )}
        </div>
        <span
          style={{
            font: "500 12px 'IBM Plex Mono'",
            color: "var(--tx)",
            lineHeight: 1.55,
            wordBreak: "break-all",
          }}
        >
          {target}
        </span>
        <div style={{ display: "flex", gap: 14, marginTop: 2 }}>
          <Metric label="chunks" value={String(node.chunks)} />
          <Metric label={t("knowledge.node.neighbours")} value={String(node.near.length)} />
          <Metric label="scope" value={node.scope ?? "project"} />
        </div>
      </div>

      {/* What is actually in the index for this node, and — under a trace —
          what the run actually received from it.
          The two are different questions and the screen keeps them apart. The
          passages are evidence about the run: this stage was handed this text.
          The indexed content is evidence about the index: this is what a search
          could ever return. Showing the document where a passage belongs would
          overstate what informed the agent, which is the same overclaim the
          "reached, not read" wording elsewhere exists to avoid. */}
      <Section
        title={t("knowledge.node.content")}
        meta={passages && passages.length ? t("knowledge.node.passageCount", { count: passages.length }) : undefined}
      >
        {passages && passages.length > 0 ? (
          passages.slice(0, 4).map((p, i) => (
            <div
              key={`${p.stage}-${i}`}
              onClick={() => setFull({ title: `${p.stage} — ${p.query || t("knowledge.node.noQuery")}`, body: p.text })}
              style={{ background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "8px 10px", display: "flex", flexDirection: "column", gap: 5, cursor: "pointer" }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "var(--cyan)", flex: "none" }}>{p.stage}</span>
                <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {p.query}
                </span>
                {typeof p.score === "number" ? (
                  <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>{p.score.toFixed(2)}</span>
                ) : null}
              </div>
              <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx3)", lineHeight: 1.6, display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical", overflow: "hidden" }}>
                {p.text}
              </span>
            </div>
          ))
        ) : passages ? (
          <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.7 }}>
            {traced ? t("knowledge.node.reachedNoText") : t("knowledge.node.noPassages")}
          </span>
        ) : null}
        {passages && passages.length > 4 ? (
          <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
            {t("knowledge.node.morePassages", { count: passages.length - 4 })}
          </span>
        ) : null}

        {/* The index's own copy, on request. For an image this is the whole
            reason to ask: it has no text of its own, so the descriptor and the
            caption are all there is to read. */}
        {indexed === null ? (
          <Button
            disabled={reading}
            onClick={async () => {
              setReading(true);
              setReadErr("");
              try {
                setIndexed((await rag.source(node.source)).text);
              } catch (e) {
                setReadErr(e instanceof Error ? e.message : String(e));
              } finally {
                setReading(false);
              }
            }}
          >
            {reading ? t("common.loading") : t("knowledge.node.showIndexed")}
          </Button>
        ) : (
          <div
            onClick={() => setFull({ title: t("knowledge.node.indexedTitle", { source: shortLabel(node.source) }), body: indexed })}
            style={{ background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "8px 10px", cursor: "pointer", display: "flex", flexDirection: "column", gap: 5 }}
          >
            <span style={{ ...railHeading, fontSize: 8.5 }}>{t("knowledge.node.indexedLabel")}</span>
            <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx3)", lineHeight: 1.6, display: "-webkit-box", WebkitLineClamp: 6, WebkitBoxOrient: "vertical", overflow: "hidden", whiteSpace: "pre-wrap" }}>
              {indexed.trim() || t("knowledge.node.indexedEmpty")}
            </span>
          </div>
        )}
        {readErr ? <Notice tone="error">{readErr}</Notice> : null}
      </Section>

      {full ? <FullText title={full.title} body={full.body} onClose={() => setFull(null)} /> : null}
      {picking && onChanged ? (
        <GroupPicker
          source={node.source}
          label={node.rel || shortLabel(node.source)}
          allGroups={allGroups}
          onClose={() => setPicking(false)}
          onSave={async (picked) => {
            setAssignErr("");
            // Only the groups whose membership actually changed are written.
            // setLinks replaces a group's whole source set, so touching one that
            // did not change would rewrite it from a list read moments ago —
            // and quietly undo an edit made in between.
            const changed = allGroups.filter(
              (g) => g.sources.includes(node.source) !== picked.has(g.id),
            );
            try {
              for (const g of changed) {
                const next = picked.has(g.id)
                  ? [...g.sources, node.source]
                  : g.sources.filter((x) => x !== node.source);
                await knowledge.setLinks(g.id, { sources: next });
              }
            } catch (e) {
              // Reported, and the reload still happens: some of the writes may
              // have landed, and the panel must show what is actually stored
              // rather than what was asked for.
              setAssignErr(e instanceof Error ? e.message : String(e));
            }
            setPicking(false);
            await onChanged();
          }}
        />
      ) : null}

      <Section
        title={t("knowledge.node.memberGroups")}
        meta={groups.length ? `${groups.length}` : t("common.none")}
        action={
          onChanged ? (
            <Button disabled={allGroups.length === 0} onClick={() => setPicking(true)}>
              {t("common.edit")}
            </Button>
          ) : undefined
        }
      >
        {assignErr ? <Notice tone="error">{assignErr}</Notice> : null}
        {groups.length === 0 ? (
          <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.7 }}>
            {t("knowledge.node.noGroup")}
          </span>
        ) : (
          groups.map((g) => (
            <div
              key={g.id}
              style={{
                background: "var(--bg-card)",
                border: "1px solid var(--bd2)",
                borderRadius: 9,
                padding: "9px 10px",
                display: "flex",
                flexDirection: "column",
                gap: 6,
              }}
            >
              <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                <Swatch color={g.color} />
                <span style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)", flex: 1 }}>
                  {g.name}
                </span>
              </div>
              <div
                style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: "3px 9px" }}
              >
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                  tag
                </span>
                <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)" }}>
                  {g.id}
                </span>
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                  nodes
                </span>
                <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)" }}>
                  {g.sources.length}
                </span>
                {g.owner ? (
                  <>
                    <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                      owner
                    </span>
                    <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)" }}>
                      {g.owner}
                    </span>
                  </>
                ) : null}
              </div>
            </div>
          ))
        )}
      </Section>

      <Section title={t("knowledge.node.source")}>
        <div style={{ display: "flex", gap: 6 }}>
          <Button
            onClick={() => {
              navigator.clipboard?.writeText(target);
              setCopied(true);
              window.setTimeout(() => setCopied(false), 1400);
            }}
          >
            {t(copied ? "common.copied" : "knowledge.node.copyPath")}
          </Button>
          {external ? (
            <Button tone="accent" onClick={() => window.open(target, "_blank", "noreferrer")}>
              {t("knowledge.node.openInBrowser")}
            </Button>
          ) : null}
        </div>
        <span
          style={{ font: "400 9.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}
        >
          {t("knowledge.node.editUpstream")}
        </span>
        <Button onClick={onReindex}>{t("knowledge.node.rebuildIndex")}</Button>
      </Section>

      <Section title={t("knowledge.node.nearestSources")} meta={`cos`}>
        {node.near.slice(0, 6).map((nb) => {
          const other = nodes[nb.to];
          if (!other) return null;
          return (
            <div
              key={nb.to}
              onClick={() => onPick(nb.to)}
              style={{
                ...selectableRow(false),
                background: "var(--bg-card)",
                border: "1px solid var(--bd2)",
              }}
            >
              <span
                style={{
                  font: "500 10px 'IBM Plex Mono'",
                  color: "var(--tx3)",
                  flex: 1,
                  minWidth: 0,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {shortLabel(other.source)}
              </span>
              <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--ac)" }}>
                {nb.score.toFixed(3)}
              </span>
            </div>
          );
        })}
      </Section>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
      <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{label}</span>
      <span style={{ font: "600 12px 'IBM Plex Mono'", color: "var(--tx2)" }}>{value}</span>
    </div>
  );
}
