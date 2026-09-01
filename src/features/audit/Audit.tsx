import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { gateway, type AccessLog, type GatewayMetrics } from "@/lib/gateway";
import { sandbox, type RunStatus } from "@/lib/sandbox";
import { isDesktop } from "@/lib/providers";
import { fetchRunLabels, type RunLabel } from "@/lib/runLabels";
import { buildLiveTree, fmtTokens, type Kind, type Layer, type Node, type Part } from "./a2a";
import { summarizeRuns, diffRuns, isBetter, type RunSummary, type Delta } from "./runDiff";

/* ── A2A kind palette ─────────────────────────────────────────────── */
const KIND: Record<Kind, { c: string; bg: string; bd: string }> = {
  Context: { c: "#34d3e0", bg: "var(--tint-active)", bd: "var(--tint-active-bd)" },
  Task: { c: "var(--ac)", bg: "var(--tint-blue)", bd: "var(--tint-blue-bd)" },
  Artifact: { c: "var(--green)", bg: "var(--tint-green)", bd: "var(--tint-green-bd)" },
  Message: { c: "var(--purple)", bg: "var(--tint-purple)", bd: "var(--bd2)" },
  Part: { c: "var(--amber)", bg: "var(--tint-amber)", bd: "var(--bd2)" },
  Extensions: { c: "#b08ad9", bg: "var(--tint-purple)", bd: "var(--bd2)" },
  Metadata: { c: "var(--tx-dim)", bg: "var(--bg-inset2)", bd: "var(--bd3)" },
};

function kindBadge(kind: Kind): React.CSSProperties {
  const k = KIND[kind];
  return {
    font: "600 8px 'IBM Plex Mono'",
    color: k.c,
    background: k.bg,
    border: `1px solid ${k.bd}`,
    padding: "1px 5px",
    borderRadius: 4,
    letterSpacing: "0.4px",
    textTransform: "uppercase",
    flex: "none",
  };
}

/* ── run list (A2A contexts) ──────────────────────────────────────── */
type RunType = "Delivery" | "Daily";
interface Run {
  id: string;
  type: RunType;
  title: string;
  ctxId: string;
  meta: string;
  tokens: string;
}
const RUNS: Run[] = [
  { id: "r1", type: "Delivery", title: "web-app indexer fix", ctxId: "ctx-8f2a41", meta: "12 tasks · 3 agents", tokens: "312K" },
  { id: "r2", type: "Delivery", title: "api rate-limit patch", ctxId: "ctx-77bd0e", meta: "5 tasks · 2 agents", tokens: "98K" },
  { id: "r3", type: "Daily", title: "Daily standup サマリ", ctxId: "ctx-2a9f13", meta: "3 tasks · 1 agent", tokens: "41K" },
  { id: "r4", type: "Daily", title: "Daily メトリクス収集", ctxId: "ctx-0c4e88", meta: "2 tasks · 1 agent", tokens: "27K" },
];

/* ── tree model (Part/Node/Kind/Layer imported from ./a2a) ────────── */

// LAYER_COLOR distinguishes the agent's reasoning (thinking) from its actions
// (tool_use) and output (text) in the live A2A tree.
const LAYER_COLOR: Record<Layer, string> = {
  system: "var(--tx-dim)",
  input: "#4f9dff",
  thinking: "#b08ad9",
  tool_use: "#e0a83e",
  text: "#3fbf8f",
  raw: "var(--tx-faint)",
};

const TREE: Node[] = [
  {
    id: "ctx", depth: 0, ancestors: [], kind: "Context", label: "web-app indexer fix", sub: "ctx-8f2a41",
    tok: "312K", hasChildren: true, ts: "10:02:11",
    fields: [
      { k: "contextId", v: "ctx-8f2a41" },
      { k: "kind", v: "context" },
      { k: "createdAt", v: "2026-07-08T10:02:11Z" },
      { k: "taskCount", v: "12" },
      { k: "agents", v: "planner, coder, reviewer" },
    ],
    ext: [{ k: "x-orchestra/token-budget", v: "1_200_000 (48% used)" }],
  },
  {
    id: "t1", depth: 1, ancestors: ["ctx"], kind: "Task", label: "index rebuild batching", sub: "task-01",
    tok: "184K", hasChildren: true, ts: "10:02:12",
    fields: [
      { k: "taskId", v: "task-01" },
      { k: "state", v: "completed" },
      { k: "assignee", v: "coder" },
      { k: "parentTask", v: "—" },
      { k: "elapsed", v: "3m 41s" },
    ],
    ext: [{ k: "x-orchestra/priority", v: "high" }],
  },
  {
    id: "m1", depth: 2, ancestors: ["ctx", "t1"], kind: "Message", label: "planning request", sub: "role: user",
    tok: "6.2K", hasChildren: true, ts: "10:02:12",
    fields: [
      { k: "messageId", v: "msg-4471" },
      { k: "role", v: "user" },
      { k: "parts", v: "1" },
    ],
    parts: [
      {
        ptype: "text", label: "rebuild the index in batches of 500", crumb: "task-01 › msg-4471 › part[0]",
        fields: [{ k: "type", v: "text" }, { k: "bytes", v: "148" }],
        body: { kind: "text", content: "インデックスの再構築を 500 件ずつのバッチに分割してください。メモリ使用量を抑えつつ、途中で失敗しても再開できるようにします。" },
      },
    ],
  },
  {
    id: "a1", depth: 2, ancestors: ["ctx", "t1"], kind: "Artifact", label: "indexer.ts パッチ", sub: "artifact-01 · +4 −1",
    tok: "9.8K", hasChildren: true, ts: "10:03:40",
    fields: [
      { k: "artifactId", v: "artifact-01" },
      { k: "name", v: "indexer.ts" },
      { k: "mimeType", v: "text/x-typescript" },
      { k: "parts", v: "1" },
    ],
    parts: [
      {
        ptype: "file", label: "indexer.ts", crumb: "task-01 › artifact-01 › part[0]",
        fields: [{ k: "type", v: "file" }, { k: "uri", v: "src/indexer.ts" }],
        body: {
          kind: "code",
          content:
            "@@ -18,7 +18,12 @@ async function rebuildIndex()\n  const docs = await db.fetchAll();\n+ const batched = chunk(docs, 500);\n+ for (const part of batched) {\n+   await index.bulk(part);\n- await index.bulk(docs);\n+ }\n  await db.commit();",
        },
      },
    ],
  },
  {
    id: "m2", depth: 2, ancestors: ["ctx", "t1"], kind: "Message", label: "実行結果", sub: "role: agent",
    tok: "3.1K", hasChildren: true, ts: "10:03:41",
    fields: [
      { k: "messageId", v: "msg-4472" },
      { k: "role", v: "agent" },
      { k: "parts", v: "1" },
    ],
    parts: [
      {
        ptype: "data", label: "result payload", crumb: "task-01 › msg-4472 › part[0]",
        fields: [{ k: "type", v: "data" }, { k: "schema", v: "task.result" }],
        body: { kind: "json", content: '{\n  "status": "completed",\n  "filesChanged": 1,\n  "additions": 4,\n  "deletions": 1,\n  "ciPassed": true\n}' },
      },
    ],
  },
  {
    id: "t2", depth: 1, ancestors: ["ctx"], kind: "Task", label: "スキーマ移行検証", sub: "task-02",
    tok: "72K", hasChildren: true, ts: "10:06:03",
    fields: [
      { k: "taskId", v: "task-02" },
      { k: "state", v: "completed" },
      { k: "assignee", v: "reviewer" },
      { k: "elapsed", v: "1m 12s" },
    ],
  },
  {
    id: "m3", depth: 2, ancestors: ["ctx", "t2"], kind: "Message", label: "レビュー承認", sub: "role: agent",
    tok: "2.4K", hasChildren: false, ts: "10:07:15",
    fields: [
      { k: "messageId", v: "msg-4480" },
      { k: "role", v: "agent" },
      { k: "parts", v: "1" },
    ],
    parts: [
      {
        ptype: "text", label: "LGTM — merge 可", crumb: "task-02 › msg-4480 › part[0]",
        fields: [{ k: "type", v: "text" }, { k: "bytes", v: "42" }],
        body: { kind: "text", content: "差分を確認しました。バッチ処理は問題ありません。マージして構いません。" },
      },
    ],
  },
  {
    id: "ext", depth: 1, ancestors: ["ctx"], kind: "Extensions", label: "x-orchestra 拡張", sub: "2 keys",
    tok: "0.4K", hasChildren: false, ts: "10:02:11",
    fields: [
      { k: "namespace", v: "x-orchestra" },
      { k: "keys", v: "token-budget, priority" },
    ],
    ext: [
      { k: "x-orchestra/token-budget", v: "1_200_000" },
      { k: "x-orchestra/compression", v: "enabled (−38%)" },
    ],
  },
  {
    id: "meta", depth: 1, ancestors: ["ctx"], kind: "Metadata", label: "trace metadata", sub: "traceId 9a1f…",
    tok: "0.1K", hasChildren: false, ts: "10:02:11",
    fields: [
      { k: "traceId", v: "9a1f2c7b40e8" },
      { k: "protocol", v: "A2A/0.3" },
      { k: "transport", v: "grpc" },
    ],
  },
];

/** When a run happened, at the precision that matters here.
 *
 *  Date included, always. This list spans whatever the audit store still holds
 *  — weeks — and a bare clock time would read as "today" for a run from a
 *  fortnight ago, which is exactly how an old trace gets mistaken for a fresh
 *  one. */
function runWhen(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** How many traceable runs the rail shows before asking. */
const TRACE_PREVIEW = 6;

const GRAN: Kind[] = ["Context", "Task", "Artifact", "Message", "Part", "Extensions", "Metadata"];
// "all" is the no-filter sentinel; it is an id, so it survives a language switch.
const ALL_AGENTS = "all";
const AGENTS = [ALL_AGENTS, "planner", "coder", "reviewer", "daily"];

/* ── metrics data ─────────────────────────────────────────────────── */
const M_CARDS = [
  { label: "消費トークン", value: "1.24M", sub: "今週 +12%" },
  { label: "セッション数", value: "48", sub: "過去7日" },
  { label: "平均セッション時間", value: "6m 42s", sub: "中央値 4m 10s" },
  { label: "コンテキスト数", value: "132", sub: "Delivery 88 / Daily 44" },
  { label: "圧縮による節約", value: "38%", sub: "−472K tokens" },
  { label: "平均コスト / セッション", value: "$0.21", sub: "先週比 −8%" },
];
const M_BARS = [
  { name: "planner", tok: 420, grad: "linear-gradient(90deg,#4f9dff,#34d3e0)" },
  { name: "coder", tok: 680, grad: "linear-gradient(90deg,#34d3e0,#3fbf8f)" },
  { name: "reviewer", tok: 210, grad: "linear-gradient(90deg,#b08ad9,#4f9dff)" },
  { name: "daily", tok: 190, grad: "linear-gradient(90deg,#e0a83e,#e06a6a)" },
];
const M_COMP = [
  { label: "prompt", pct: "46%", w: 46, color: "#4f9dff" },
  { label: "completion", pct: "28%", w: 28, color: "#34d3e0" },
  { label: "cached", pct: "18%", w: 18, color: "#3fbf8f" },
  { label: "tools", pct: "8%", w: 8, color: "#b08ad9" },
];
const M_SPARK = [40, 62, 48, 74, 55, 88, 70];
const M_SESSIONS = [
  { name: "web-app indexer fix", meta: "Delivery · 12 tasks", when: "2h前", tok: "312K", dur: "9m 21s" },
  { name: "api rate-limit patch", meta: "Delivery · 5 tasks", when: "5h前", tok: "98K", dur: "3m 44s" },
  { name: "Daily standup サマリ", meta: "Daily · 3 tasks", when: "昨日", tok: "41K", dur: "1m 08s" },
];

// Demo run summaries for the optimize tab when not connected live: two runs of
// the same task (v1 baseline → v2 after prompt/context tuning) plus one other.
const MOCK_SUMMARIES: RunSummary[] = [
  { run: "run-indexer-v2", calls: 6, tokensEst: 198_000, inputTokens: 142_000, outputTokens: 56_000, toolCalls: 4, errors: 0, wallMs: 214_000, avgDurationMs: 5_200, models: ["claude-opus-4-8"], stages: [{ stage: "plan", calls: 1, tokensEst: 24_000 }, { stage: "code", calls: 3, tokensEst: 132_000 }, { stage: "review", calls: 2, tokensEst: 42_000 }], firstTs: "2026-07-12T10:20:00Z", lastTs: "2026-07-12T10:23:34Z" },
  { run: "run-indexer-v1", calls: 9, tokensEst: 312_000, inputTokens: 236_000, outputTokens: 76_000, toolCalls: 7, errors: 1, wallMs: 561_000, avgDurationMs: 6_800, models: ["claude-opus-4-8"], stages: [{ stage: "plan", calls: 2, tokensEst: 41_000 }, { stage: "code", calls: 5, tokensEst: 214_000 }, { stage: "review", calls: 2, tokensEst: 57_000 }], firstTs: "2026-07-12T09:02:11Z", lastTs: "2026-07-12T09:11:32Z" },
  { run: "run-rate-limit", calls: 5, tokensEst: 98_000, inputTokens: 70_000, outputTokens: 28_000, toolCalls: 3, errors: 0, wallMs: 224_000, avgDurationMs: 5_100, models: ["claude-sonnet-5"], stages: [{ stage: "code", calls: 3, tokensEst: 70_000 }, { stage: "review", calls: 2, tokensEst: 28_000 }], firstTs: "2026-07-12T08:00:00Z", lastTs: "2026-07-12T08:03:44Z" },
];

/* ── live (gateway) helpers ───────────────────────────────────────── */
const BAR_GRADS = [
  "linear-gradient(90deg,#4f9dff,#34d3e0)",
  "linear-gradient(90deg,#34d3e0,#3fbf8f)",
  "linear-gradient(90deg,#b08ad9,#4f9dff)",
  "linear-gradient(90deg,#e0a83e,#e06a6a)",
];

function fmtLogTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString("ja-JP", { hour12: false });
}

function statusBadge(status: number): React.CSSProperties {
  let c = "var(--green)", bg = "var(--tint-green)", bd = "var(--tint-green-bd)";
  if (status >= 500 || status === 0) { c = "var(--red)"; bg = "var(--tint-red)"; bd = "var(--tint-red-bd)"; }
  else if (status >= 400) { c = "var(--amber)"; bg = "var(--tint-amber)"; bd = "var(--bd2)"; }
  return { font: "600 9px 'IBM Plex Mono'", color: c, background: bg, border: `1px solid ${bd}`, padding: "1px 6px", borderRadius: 4, flex: "none", width: 34, textAlign: "center" };
}

const methodBadgeStyle: React.CSSProperties = {
  font: "600 8.5px 'IBM Plex Mono'", color: "var(--ac)", background: "var(--tint-blue)",
  border: "1px solid var(--tint-blue-bd)", padding: "1px 5px", borderRadius: 4, flex: "none",
  width: 44, textAlign: "center", letterSpacing: "0.3px",
};

// ContentBlock renders one captured prompt/response body (pretty JSON if it parses).
function ContentBlock({ label, text }: { label: string; text: string }) {
  let body = text;
  try { body = JSON.stringify(JSON.parse(text), null, 2); } catch { /* keep raw */ }
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
      <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.3px" }}>{label}</span>
      <pre style={{ margin: 0, maxHeight: 200, overflow: "auto", background: "var(--bg-deep)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "8px 10px", fontFamily: "'IBM Plex Mono',monospace", fontSize: 10, lineHeight: 1.6, color: "var(--tx3)", whiteSpace: "pre-wrap" }}>{body}</pre>
    </div>
  );
}

function AuditLiveToggle({ live, error, onToggle }: { live: boolean; error: string | null; onToggle: () => void }) {
  const { t } = useTranslation();
  return (
    <div
      onClick={onToggle}
      title={error ?? t(live ? "audit.gatewayConnected" : "audit.gatewayConnect")}
      style={{
        display: "flex", alignItems: "center", gap: 7, cursor: "pointer",
        padding: "5px 11px", borderRadius: 7,
        border: `1px solid ${live ? "var(--tint-green-bd)" : error ? "var(--tint-red-bd)" : "var(--bd2)"}`,
        background: live ? "var(--tint-green)" : error ? "var(--tint-red)" : "var(--bg-card2)",
      }}
    >
      <span className={live ? "oc-active-dot" : undefined} style={{ width: 7, height: 7, borderRadius: "50%", background: live ? "var(--green)" : error ? "var(--red)" : "var(--tx-dim)" }} />
      <span style={{ font: "500 11px 'IBM Plex Mono'", color: live ? "#67c9a4" : error ? "var(--red)" : "var(--tx3)" }}>
        {live ? "live · gateway" : "mock data"}
      </span>
    </div>
  );
}

/* ── component ────────────────────────────────────────────────────── */
export function Audit() {
  const { t } = useTranslation();
  const [runType, setRunType] = useState<"all" | RunType>("all");
  const [selectedRun, setSelectedRun] = useState("r1");
  const [tab, setTab] = useState<"logs" | "metrics" | "optimize">("logs");
  const [liveView, setLiveView] = useState<"tree" | "raw">("tree");

  // logs state
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(["ctx", "t1", "t2"]));
  const [selectedNode, setSelectedNode] = useState("ctx");
  const [granActive, setGranActive] = useState<Set<Kind>>(() => new Set(GRAN));
  const [agentDD, setAgentDD] = useState(false);
  const [agent, setAgent] = useState(ALL_AGENTS);
  const [taskQuery, setTaskQuery] = useState("");
  const [preset, setPreset] = useState("all");
  const [dtFrom, setDtFrom] = useState("");
  const [dtTo, setDtTo] = useState("");

  // part drawer
  const [part, setPart] = useState<Part | null>(null);
  const [partFull, setPartFull] = useState(false);

  // Live gateway data. On the desktop shell the gateway is always running and
  // its logs are the whole point of this screen, so start live — defaulting to
  // false meant the app silently showed mock data until the header toggle was
  // found, and the toggle is easy to read as a status light rather than a
  // control. In the browser there is no Tauri invoker to reach the admin API,
  // so the mock stays.
  const [live, setLive] = useState(isDesktop);
  const [liveError, setLiveError] = useState<string | null>(null);
  const [liveLogs, setLiveLogs] = useState<AccessLog[]>([]);
  const [liveMetrics, setLiveMetrics] = useState<GatewayMetrics | null>(null);
  const [openLog, setOpenLog] = useState<string | null>(null);
  // Run status per run id. Artifacts are filesystem effects, so they are absent
  // from the gateway capture and have to be joined in from the orchestrator.
  const [liveRuns, setLiveRuns] = useState<Map<string, RunStatus>>(new Map());

  // live A2A tree, rebuilt from the gateway capture on each poll
  const liveTree = useMemo(() => buildLiveTree(liveLogs, liveRuns), [liveLogs, liveRuns]);
  const showLiveTree = live && liveView === "tree";

  // auto-expand newly-seen Context/Task nodes once (so new runs open, but a
  // user's collapse sticks across polls)
  const seededRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!live) return;
    setExpanded((prev) => {
      let changed = false;
      const next = new Set(prev);
      for (const n of liveTree) {
        if ((n.kind === "Context" || n.kind === "Task") && !seededRef.current.has(n.id)) {
          seededRef.current.add(n.id);
          next.add(n.id);
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [liveTree, live]);

  const navigate = useNavigate();

  // Runs the gateway actually saw, newest first. These are the ids a trace can
  // be built for — distinct from the sample contexts listed above, which exist
  // so the screen reads sensibly with no live data.
  //
  // The gateway serves its records newest-first (ORDER BY seq DESC), so this
  // walks forwards and takes each run at its first sighting. It used to walk
  // backwards, which put the OLDEST runs at the top — and with only the first
  // six shown, an audit store holding weeks meant the rail offered a fortnight
  // -old run as the obvious thing to open. That is not a cosmetic ordering
  // bug: a trace read as today's when it is a fortnight old says the wrong
  // thing about what a run reached.
  const tracableRuns = useMemo(() => {
    const order: string[] = [];
    const stats = new Map<string, { first: string; retrievals: number }>();
    for (const l of liveLogs) {
      if (!l.run) continue;
      let st = stats.get(l.run);
      if (!st) {
        order.push(l.run);
        st = { first: l.time, retrievals: 0 };
        stats.set(l.run, st);
      }
      // Records arrive newest-first, so the last one seen for a run is its
      // earliest — which is when the run began, and the date a reader wants.
      if (l.time < st.first) st.first = l.time;
      if (l.service === "rag") st.retrievals++;
    }
    return order.map((id) => ({
      id,
      time: stats.get(id)!.first,
      retrievals: stats.get(id)!.retrievals,
    }));
  }, [liveLogs]);

  // What each of those runs was, in a person's terms. The log only carries the
  // orchestrator id, and an id is not a reason to open a trace.
  //
  // Refetched when a run appears that the current lookup cannot name, rather
  // than on the three-second poll beside it: run histories change when a run
  // starts, not continuously, and a name that has not arrived yet costs a row
  // its title for one poll rather than costing every poll two requests.
  const [runLabels, setRunLabels] = useState<Map<string, RunLabel>>(new Map());
  // The audit store keeps weeks of records, so this list grows without bound
  // while the rail it sits in is a fixed column. Six is what fits above the
  // context list without pushing it off screen; the rest are one click away
  // rather than gone.
  const [allTraces, setAllTraces] = useState(false);
  // Keyed by WHICH runs are unnamed, not merely whether any are. A run nobody
  // recorded stays unnamed forever, and a boolean would sit true from then on —
  // so every run started afterwards would inherit that "already asked" and
  // never get looked up at all.
  const unnamedKey = tracableRuns
    .filter((r) => !runLabels.has(r.id))
    .map((r) => r.id)
    .join(",");
  useEffect(() => {
    if (!live || !unnamedKey) return;
    let cancelled = false;
    fetchRunLabels()
      .then((m) => {
        if (!cancelled) setRunLabels(m);
      })
      // A run nobody recorded is normal — a delegated sub-agent, a run launched
      // before the history existed — and it simply keeps showing its id.
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [live, unnamedKey]);

  // optimize (run-diff) state
  const runSummaries = useMemo<RunSummary[]>(() => (live ? summarizeRuns(liveLogs) : MOCK_SUMMARIES), [live, liveLogs]);
  const [baseRun, setBaseRun] = useState<string | null>(null);
  const [candRun, setCandRun] = useState<string | null>(null);

  useEffect(() => {
    if (!live) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const [l, m] = await Promise.all([gateway.logs(), gateway.metrics()]);
        if (cancelled) return;
        setLiveLogs(l); setLiveMetrics(m); setLiveError(null);

        // Join in the orchestrator's view for the runs these logs mention. A run
        // the controller no longer holds simply contributes no artifacts, so a
        // failed lookup is skipped rather than surfaced as a gateway error.
        const runIds = Array.from(new Set(l.map((x) => x.run).filter((r): r is string => !!r)));
        const statuses = await Promise.all(
          runIds.map((id) => sandbox.runStatus(id).then((st) => [id, st] as const).catch(() => null)),
        );
        if (cancelled) return;
        setLiveRuns(new Map(statuses.filter((x): x is readonly [string, RunStatus] => x !== null)));
      } catch (e) {
        if (!cancelled) setLiveError(e instanceof Error ? e.message : t("audit.connectError"));
      }
    };
    poll();
    const id = setInterval(poll, 3000);
    return () => { cancelled = true; clearInterval(id); };
  }, [live]);

  const metricCards = live && liveMetrics
    ? [
        { label: t("audit.metrics.totalRequests"), value: String(liveMetrics.totalRequests), sub: t("audit.metrics.gatewayTotal") },
        { label: t("audit.metrics.estTokens"), value: fmtTokens(liveMetrics.totalTokensEst), sub: t("audit.metrics.cumulative") },
        { label: t("audit.metrics.sessions"), value: String(liveMetrics.sessions), sub: "distinct session ids" },
      ]
    : M_CARDS;

  const liveBars = live && liveMetrics
    ? Object.entries(liveMetrics.perService).map(([name, v], i) => ({ name, value: v.requests, label: `${v.requests}`, grad: BAR_GRADS[i % BAR_GRADS.length] }))
    : M_BARS.map((b) => ({ name: b.name, value: b.tok, label: `${b.tok}K`, grad: b.grad }));
  const barMax = Math.max(1, ...liveBars.map((b) => b.value));

  const runs = RUNS.filter((r) => runType === "all" || r.type === runType);

  const activeTree = showLiveTree ? liveTree : TREE;

  const visibleRows = useMemo(() => {
    const q = taskQuery.trim().toLowerCase();
    return activeTree.filter((n) => {
      if (!n.ancestors.every((a) => expanded.has(a))) return false;
      if (!granActive.has(n.kind)) return false;
      if (q && !(n.label.toLowerCase().includes(q) || n.sub.toLowerCase().includes(q))) return false;
      return true;
    });
  }, [activeTree, expanded, granActive, taskQuery]);

  const node: Node | undefined = activeTree.find((n) => n.id === selectedNode) ?? activeTree[0];
  const nk = node ? KIND[node.kind] : KIND.Context;

  const scopeFiltered = taskQuery.trim() !== "" || agent !== ALL_AGENTS;
  const timeFiltered = preset !== "all";

  const toggleNode = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const toggleGran = (k: Kind) => {
    setGranActive((prev) => {
      const next = new Set(prev);
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  };

  const typeBtn = (active: boolean): React.CSSProperties => ({
    font: "500 10px 'IBM Plex Mono'",
    color: active ? "var(--tx)" : "var(--tx-dim)",
    background: active ? "var(--tint-active)" : "transparent",
    border: `1px solid ${active ? "var(--tint-active-bd)" : "var(--bd2)"}`,
    borderRadius: 6,
    padding: "3px 8px",
    cursor: "pointer",
  });

  const tabBtn = (active: boolean): React.CSSProperties => ({
    font: "600 12px 'IBM Plex Sans'",
    color: active ? "var(--tx)" : "var(--tx-dim)",
    padding: "6px 4px",
    borderBottom: `2px solid ${active ? "var(--ac)" : "transparent"}`,
    cursor: "pointer",
  });

  return (
    <div style={{ flex: 1, display: "flex", minHeight: 0, position: "relative" }}>
      {/* ── run list ────────────────────────────────────────────── */}
      <div style={{ width: 236, flex: "none", background: "var(--bg-panel)", borderRight: "1px solid var(--bd)", padding: "16px 12px", display: "flex", flexDirection: "column", gap: 12, overflowY: "auto" }}>
        <div style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>A2A Contexts</div>
        <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
          <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px", marginRight: 2 }}>{t("audit.kind")}</span>
          <div onClick={() => setRunType("all")} style={typeBtn(runType === "all")}>all</div>
          <div onClick={() => setRunType("Delivery")} style={typeBtn(runType === "Delivery")}>Delivery</div>
          <div onClick={() => setRunType("Daily")} style={typeBtn(runType === "Daily")}>Daily</div>
        </div>
        {tracableRuns.length > 0 ? (
          <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
            <div style={{ font: "600 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px" }}>
              {t("audit.knowledgeTrace")}
            </div>
            {(allTraces ? tracableRuns : tracableRuns.slice(0, TRACE_PREVIEW)).map((r) => {
              const label = runLabels.get(r.id);
              return (
                <div
                  key={r.id}
                  onClick={() => navigate(`/knowledge?run=${encodeURIComponent(r.id)}`)}
                  style={{ display: "flex", flexDirection: "column", gap: 3, padding: "7px 9px", borderRadius: 7, cursor: "pointer", background: "var(--bg-card)", border: "1px solid var(--bd)" }}
                  title={label ? `${label.title}\n${label.sub ? label.sub + "\n" : ""}${r.id}\n\n${t("audit.knowledgeTraceTip")}` : `${r.id}\n\n${t("audit.knowledgeTraceTip")}`}
                >
                  <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                    {label ? (
                      <span style={{ font: "600 8px 'IBM Plex Mono'", color: label.kind === "Daily" ? "#e0a83e" : "#34d3e0", border: `1px solid ${label.kind === "Daily" ? "#e0a83e" : "#34d3e0"}`, borderRadius: 3, padding: "0 3px", flex: "none" }}>
                        {label.kind}
                      </span>
                    ) : null}
                    {/* The title when we have one, the id when we do not — never
                        both in this slot, so the row's first line is always the
                        most identifying thing known about the run. */}
                    <span style={{ font: label ? "600 10px 'IBM Plex Sans'" : "500 9.5px 'IBM Plex Mono'", color: label ? "var(--tx2)" : "var(--tx3)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {label ? label.title : r.id}
                    </span>
                    <span style={{ font: "600 9px 'IBM Plex Mono'", color: r.retrievals ? "#34d3e0" : "var(--tx-faint)", flex: "none" }}>{t("audit.searchCount", { count: r.retrievals })}</span>
                  </div>
                  {/* The id stays visible even when named: it is what a log
                      search takes, and the title is what decides whether to run
                      one. */}
                  <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {[runWhen(r.time), label ? label.sub : "", label ? r.id : t("audit.unnamedRun")]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </div>
              );
            })}
            {tracableRuns.length > TRACE_PREVIEW ? (
              <div
                onClick={() => setAllTraces((v) => !v)}
                style={{ font: "500 9.5px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer", padding: "1px 2px" }}
              >
                {allTraces
                  ? t("audit.showFewerTraces")
                  : t("audit.showAllTraces", { count: tracableRuns.length })}
              </div>
            ) : null}
          </div>
        ) : null}
        <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
          {runs.map((r) => {
            const active = r.id === selectedRun;
            return (
              <div
                key={r.id}
                onClick={() => setSelectedRun(r.id)}
                style={{ display: "flex", flexDirection: "column", gap: 5, padding: "9px 11px", borderRadius: 8, cursor: "pointer", background: active ? "var(--tint-active)" : "var(--bg-card)", border: `1px solid ${active ? "var(--tint-active-bd)" : "var(--bd)"}` }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                  <span style={kindBadge(r.type === "Delivery" ? "Task" : "Metadata")}>{r.type === "Delivery" ? "DLV" : "DLY"}</span>
                  <span style={{ font: "600 11.5px 'IBM Plex Sans'", color: active ? "var(--tx)" : "var(--tx2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{r.title}</span>
                </div>
                <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{r.ctxId}</span>
                <div style={{ display: "flex", alignItems: "center", gap: 7, font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                  <span>{r.meta}</span>
                  <span style={{ marginLeft: "auto", color: "#34d3e0" }}>{r.tokens}</span>
                </div>
              </div>
            );
          })}
        </div>
      </div>

      {/* ── main ────────────────────────────────────────────────── */}
      <div style={{ flex: 1, display: "flex", flexDirection: "column", minWidth: 0, background: "var(--bg-app)" }}>
        <div style={{ height: 52, flex: "none", display: "flex", alignItems: "center", padding: "0 20px", gap: 10, borderBottom: "1px solid var(--bd)" }}>
          <div onClick={() => setTab("logs")} style={tabBtn(tab === "logs")}>{t("audit.tabs.logs")}</div>
          <div onClick={() => setTab("metrics")} style={tabBtn(tab === "metrics")}>{t("audit.tabs.metrics")}</div>
          <div onClick={() => setTab("optimize")} style={tabBtn(tab === "optimize")}>{t("audit.tabs.optimize")}</div>
          <div style={{ flex: 1 }} />
          {live && tab === "logs" && (
            <div style={{ display: "flex", border: "1px solid var(--bd2)", borderRadius: 7, overflow: "hidden" }}>
              {(["tree", "raw"] as const).map((v) => (
                <div
                  key={v}
                  onClick={() => setLiveView(v)}
                  style={{ font: "500 10px 'IBM Plex Mono'", padding: "4px 10px", cursor: "pointer", color: liveView === v ? "var(--tx)" : "var(--tx-dim)", background: liveView === v ? "var(--tint-active)" : "transparent" }}
                >
                  {t(v === "tree" ? "audit.a2aTree" : "audit.rawLog")}
                </div>
              ))}
            </div>
          )}
          <AuditLiveToggle live={live} error={liveError} onToggle={() => { setLive((v) => !v); setLiveError(null); }} />
          <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{live ? (liveView === "tree" ? "A2A trace (live capture)" : "gateway request log") : "A2A protocol trace"}</span>
        </div>

        {/* ── LOGS ─────────────────────────────────────────────── */}
        {tab === "logs" && (
          <>
            <div style={{ flex: "none", padding: "14px 22px 0", display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
              <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px", marginRight: 2 }}>{t("audit.granularity")}</span>
              {GRAN.map((g) => {
                const on = granActive.has(g);
                const k = KIND[g];
                return (
                  <div
                    key={g}
                    onClick={() => toggleGran(g)}
                    style={{ display: "flex", alignItems: "center", gap: 5, cursor: "pointer", font: "500 9.5px 'IBM Plex Mono'", color: on ? "var(--tx2)" : "var(--tx-dim)", background: on ? "var(--bg-card2)" : "transparent", border: `1px solid ${on ? "var(--bd2)" : "var(--bd)"}`, borderRadius: 6, padding: "4px 8px", opacity: on ? 1 : 0.55 }}
                  >
                    <div style={{ width: 6, height: 6, borderRadius: "50%", background: k.c }} />
                    {g}
                  </div>
                );
              })}
              <div style={{ width: 1, height: 18, background: "var(--bd-sep)", margin: "0 3px" }} />
              <div style={{ position: "relative" }}>
                <div onClick={() => setAgentDD((v) => !v)} style={{ display: "flex", alignItems: "center", gap: 5, cursor: "pointer", font: "500 10px 'IBM Plex Mono'", color: "var(--tx2)", background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "4px 9px" }}>
                  {agent === ALL_AGENTS ? t("audit.agent") : agent}
                  <span style={{ fontSize: 7, color: "var(--tx-faint)" }}>▼</span>
                </div>
                {agentDD && (
                  <div style={{ position: "absolute", top: 30, left: 0, minWidth: 178, background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 9, boxShadow: "0 12px 32px rgba(0,0,0,.4)", padding: 5, zIndex: 20, display: "flex", flexDirection: "column", gap: 1 }}>
                    {AGENTS.map((o) => {
                      const on = o === agent;
                      return (
                        <div
                          key={o}
                          onClick={() => { setAgent(o); setAgentDD(false); }}
                          style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer", font: "500 10px 'IBM Plex Mono'", color: on ? "var(--tx)" : "var(--tx3)", background: on ? "var(--tint-active)" : "transparent", borderRadius: 6, padding: "6px 9px" }}
                        >
                          <div style={{ width: 6, height: 6, borderRadius: "50%", background: on ? "var(--ac)" : "var(--tx-faint)" }} />
                          {o === ALL_AGENTS ? t("audit.all") : o}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
              <div style={{ display: "flex", alignItems: "center", gap: 6, background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "4px 9px" }}>
                <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="var(--tx-faint)" strokeWidth="1.6"><circle cx="7" cy="7" r="4.5" /><path d="M10.5 10.5L14 14" /></svg>
                <input value={taskQuery} onChange={(e) => setTaskQuery(e.target.value)} placeholder={t("audit.taskIdSearch")} style={{ border: "none", outline: "none", background: "transparent", font: "500 10px 'IBM Plex Mono'", color: "var(--tx2)", width: 110 }} />
              </div>
              {scopeFiltered && (
                <div onClick={() => { setTaskQuery(""); setAgent(ALL_AGENTS); }} style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)", cursor: "pointer", padding: "4px 9px", border: "1px solid var(--bd2)", borderRadius: 6 }}>{t("audit.clearFilter")}</div>
              )}
            </div>

            <div style={{ flex: "none", padding: "12px 22px 0", display: "flex", alignItems: "center", gap: 11, flexWrap: "wrap" }}>
              <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px", flex: "none" }}>{t("audit.period")}</span>
              <select value={preset} onChange={(e) => setPreset(e.target.value)} style={{ background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 6, color: "var(--tx2)", font: "500 10px 'IBM Plex Mono'", padding: "5px 8px", outline: "none", colorScheme: "dark", cursor: "pointer" }}>
                <option value="all">{t("audit.periods.all")}</option>
                <option value="first1m">{t("audit.periods.first1m")}</option>
                <option value="last1m">{t("audit.periods.last1m")}</option>
                <option value="first30">{t("audit.periods.first30")}</option>
                <option value="last30">{t("audit.periods.last30")}</option>
                <option value="custom">{t("audit.periods.custom")}</option>
              </select>
              <input type="datetime-local" step={1} value={dtFrom} onChange={(e) => { setDtFrom(e.target.value); setPreset("custom"); }} style={{ background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 6, color: "#34d3e0", font: "500 10px 'IBM Plex Mono'", padding: "5px 8px", outline: "none", colorScheme: "dark" }} />
              <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>〜</span>
              <input type="datetime-local" step={1} value={dtTo} onChange={(e) => { setDtTo(e.target.value); setPreset("custom"); }} style={{ background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 6, color: "#4f9dff", font: "500 10px 'IBM Plex Mono'", padding: "5px 8px", outline: "none", colorScheme: "dark" }} />
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)", flex: "none" }}>{visibleRows.length} entries</span>
              {timeFiltered && (
                <div onClick={() => { setPreset("all"); setDtFrom(""); setDtTo(""); }} style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)", cursor: "pointer", padding: "4px 9px", border: "1px solid var(--bd2)", borderRadius: 6, flex: "none" }}>{t("audit.periods.all")}</div>
              )}
            </div>

            <div style={{ flex: 1, display: "flex", minHeight: 0, padding: "14px 22px 22px", gap: 14 }}>
              {live && liveView === "raw" ? (
                <div style={{ flex: 1, minWidth: 0, background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, overflowY: "auto", padding: 8 }}>
                  {liveLogs.length === 0 ? (
                    <div style={{ padding: 20, font: "500 11px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                      {liveError ? `${t("audit.connectError")}: ${liveError}` : t("audit.noRequestLogs")}
                    </div>
                  ) : liveLogs.map((l) => {
                    const hasContent = !!(l.reqBody || l.respBody);
                    const open = openLog === l.requestId;
                    return (
                    <div key={l.requestId} style={{ borderBottom: "1px solid var(--bd-soft)" }}>
                      <div onClick={() => hasContent && setOpenLog(open ? null : l.requestId)} style={{ display: "flex", alignItems: "center", gap: 8, padding: "7px 9px", cursor: hasContent ? "pointer" : "default" }}>
                        <span style={{ font: "400 8px 'IBM Plex Mono'", color: "var(--tx-faint)", width: 9, flex: "none" }}>{hasContent ? (open ? "▾" : "▸") : ""}</span>
                        <span style={methodBadgeStyle}>{l.method}</span>
                        <span style={statusBadge(l.status)}>{l.status || "ERR"}</span>
                        <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx3)", width: 54, flex: "none" }}>{l.service}</span>
                        {l.model && <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "#67c9a4", background: "var(--tint-green)", padding: "1px 5px", borderRadius: 4, flex: "none" }}>{l.model}</span>}
                        {(l.run || l.stage) && <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "#c79ae0", background: "var(--tint-purple)", padding: "1px 5px", borderRadius: 4, flex: "none" }}>{l.stage || l.run}</span>}
                        <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx2)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{l.path}</span>
                        <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "#5b9fe8", width: 58, textAlign: "right", flex: "none" }}>{l.tokensEst ? `${l.tokensEst} tok` : "—"}</span>
                        <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)", width: 48, textAlign: "right", flex: "none" }}>{l.durationMs}ms</span>
                        <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", width: 60, textAlign: "right", flex: "none" }}>{fmtLogTime(l.time)}</span>
                      </div>
                      {open && (
                        <div style={{ display: "flex", flexDirection: "column", gap: 6, padding: "2px 12px 12px 30px" }}>
                          {((l.inputTokens ?? 0) + (l.outputTokens ?? 0)) > 0 && (
                            <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                              {t("audit.actualTokens", { input: l.inputTokens ?? 0, output: l.outputTokens ?? 0 })}
                            </span>
                          )}
                          {l.reqBody && <ContentBlock label={t("audit.requestBlock")} text={l.reqBody} />}
                          {l.respBody && <ContentBlock label={t("audit.responseBlock")} text={l.respBody} />}
                        </div>
                      )}
                    </div>
                    );
                  })}
                </div>
              ) : activeTree.length === 0 ? (
                <div style={{ flex: 1, minWidth: 0, background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, display: "flex", alignItems: "center", justifyContent: "center" }}>
                  <span style={{ padding: 20, font: "500 11px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                    {liveError ? `${t("audit.connectError")}: ${liveError}` : t("audit.noGatewayCalls")}
                  </span>
                </div>
              ) : (
              <>
              {/* tree */}
              <div style={{ flex: 1, minWidth: 0, background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, overflowY: "auto", padding: 8 }}>
                {visibleRows.map((n) => {
                  const sel = n.id === selectedNode;
                  const k = KIND[n.kind];
                  const open = expanded.has(n.id);
                  const dotColor = n.kind === "Part" ? (LAYER_COLOR[n.sub as Layer] ?? k.c) : k.c;
                  return (
                    <div
                      key={n.id}
                      onClick={() => setSelectedNode(n.id)}
                      style={{ display: "flex", alignItems: "center", gap: 7, padding: "6px 8px", paddingLeft: 8 + n.depth * 18, borderRadius: 7, cursor: "pointer", background: sel ? "var(--tint-active)" : "transparent" }}
                    >
                      <span
                        onClick={(e) => { e.stopPropagation(); if (n.hasChildren) toggleNode(n.id); }}
                        style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-dim)", width: 10, flex: "none", cursor: n.hasChildren ? "pointer" : "default" }}
                      >
                        {n.hasChildren ? (open ? "▾" : "▸") : ""}
                      </span>
                      <span style={kindBadge(n.kind)}>{n.kind}</span>
                      <div style={{ width: 6, height: 6, borderRadius: "50%", background: dotColor, flex: "none" }} />
                      <span style={{ font: "500 11px 'IBM Plex Sans'", color: sel ? "var(--tx)" : "var(--tx2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{n.label}</span>
                      <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>{n.sub}</span>
                      <span style={{ marginLeft: "auto", font: "500 9.5px 'IBM Plex Mono'", color: "#5b9fe8", flex: "none" }}>{n.tok}</span>
                    </div>
                  );
                })}
              </div>

              {/* inspector */}
              {node && (
              <div style={{ width: 268, flex: "none", background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: 16, display: "flex", flexDirection: "column", gap: 13, overflowY: "auto" }}>
                <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                  <div style={{ width: 8, height: 8, borderRadius: "50%", background: nk.c }} />
                  <span style={kindBadge(node.kind)}>{node.kind}</span>
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
                  <span style={{ font: "700 14px 'IBM Plex Mono'", color: "var(--tx)" }}>{node.label}</span>
                  <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{node.sub}</span>
                </div>
                <div style={{ display: "flex", gap: 8 }}>
                  <div style={{ flex: 1, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 8, padding: "8px 10px", display: "flex", flexDirection: "column", gap: 2 }}>
                    <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>tokens</span>
                    <span style={{ font: "600 12px 'IBM Plex Mono'", color: "#34d3e0" }}>{node.tok}</span>
                  </div>
                  <div style={{ flex: 1, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 8, padding: "8px 10px", display: "flex", flexDirection: "column", gap: 2 }}>
                    <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>timestamp</span>
                    <span style={{ font: "600 12px 'IBM Plex Mono'", color: "var(--tx2)" }}>{node.ts}</span>
                  </div>
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
                  <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px" }}>FIELDS</span>
                  {node.fields.map((f) => (
                    <div key={f.k} style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
                      <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", width: 84, flex: "none" }}>{f.k}</span>
                      <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)", wordBreak: "break-all" }}>{f.v}</span>
                    </div>
                  ))}
                </div>
                {node.parts && node.parts.length > 0 && (
                  <div style={{ display: "flex", flexDirection: "column", gap: 7, paddingTop: 11, borderTop: "1px solid var(--bd-soft)" }}>
                    <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px" }}>PARTS</span>
                    {node.parts.map((p) => (
                      <div
                        key={p.label}
                        onClick={() => { setPart(p); setPartFull(false); }}
                        style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer", background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "7px 9px" }}
                      >
                        <span style={{ font: "600 8px 'IBM Plex Mono'", color: p.layer ? LAYER_COLOR[p.layer] : "var(--amber)", background: "var(--bg-inset2)", border: "1px solid var(--bd2)", padding: "1px 5px", borderRadius: 4, flex: "none" }}>{p.layer ?? p.ptype}</span>
                        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx3)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: 1 }}>{p.label}</span>
                        <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="var(--tx-faint)" strokeWidth="1.6" style={{ flex: "none" }}><path d="M6 4l4 4-4 4" /></svg>
                      </div>
                    ))}
                  </div>
                )}
                {node.ext && node.ext.length > 0 && (
                  <div style={{ display: "flex", flexDirection: "column", gap: 7, paddingTop: 11, borderTop: "1px solid var(--bd-soft)" }}>
                    <span style={{ font: "600 9px 'IBM Plex Mono'", color: "#b08ad9", letterSpacing: "0.5px" }}>EXTENSIONS</span>
                    {node.ext.map((e) => (
                      <div key={e.k} style={{ display: "flex", flexDirection: "column", gap: 1 }}>
                        <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)" }}>{e.k}</span>
                        <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{e.v}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              )}
              </>
              )}
            </div>
          </>
        )}

        {/* ── METRICS ──────────────────────────────────────────── */}
        {tab === "metrics" && (
          <div style={{ flex: 1, overflowY: "auto", padding: "18px 22px", display: "flex", flexDirection: "column", gap: 16 }}>
            <div style={{ display: "grid", gridTemplateColumns: "repeat(3,1fr)", gap: 11 }}>
              {metricCards.map((m) => (
                <div key={m.label} style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 10, padding: "13px 15px", display: "flex", flexDirection: "column", gap: 4 }}>
                  <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)", letterSpacing: "0.3px" }}>{m.label}</span>
                  <span style={{ font: "700 22px 'IBM Plex Sans'", color: "var(--tx)", letterSpacing: "-0.5px" }}>{m.value}</span>
                  <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{m.sub}</span>
                </div>
              ))}
            </div>

            <div style={{ display: "grid", gridTemplateColumns: "1.3fr 1fr", gap: 14 }}>
              {/* per-agent tokens */}
              <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "16px 18px", display: "flex", flexDirection: "column", gap: 13 }}>
                <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t(live ? "audit.requestsByService" : "audit.tokensByAgent")}</span>
                <div style={{ display: "flex", flexDirection: "column", gap: 11 }}>
                  {liveBars.length === 0 ? (
                    <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("audit.noData")}</span>
                  ) : liveBars.map((a) => (
                    <div key={a.name} style={{ display: "flex", alignItems: "center", gap: 11 }}>
                      <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx3)", width: 80, flex: "none" }}>{a.name}</span>
                      <div style={{ flex: 1, height: 9, background: "var(--bd3)", borderRadius: 5, overflow: "hidden" }}>
                        <div style={{ width: `${(a.value / barMax) * 100}%`, height: "100%", background: a.grad }} />
                      </div>
                      <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)", width: 42, textAlign: "right" }}>{a.label}</span>
                    </div>
                  ))}
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: 9, paddingTop: 13, borderTop: "1px solid var(--bd-soft)" }}>
                  <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px" }}>{t("audit.tokenBreakdown")}</span>
                  <div style={{ display: "flex", height: 10, borderRadius: 5, overflow: "hidden" }}>
                    {M_COMP.map((c) => (
                      <div key={c.label} style={{ width: `${c.w}%`, background: c.color }} />
                    ))}
                  </div>
                  <div style={{ display: "flex", alignItems: "center", gap: 14, flexWrap: "wrap" }}>
                    {M_COMP.map((c) => (
                      <div key={c.label} style={{ display: "flex", alignItems: "center", gap: 6, font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                        <div style={{ width: 8, height: 8, borderRadius: 2, background: c.color }} />
                        {c.label} {c.pct}
                      </div>
                    ))}
                  </div>
                </div>
              </div>

              {/* sessions over time */}
              <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "16px 18px", display: "flex", flexDirection: "column", gap: 13 }}>
                <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("audit.sessions7d")}</span>
                <div style={{ flex: 1, display: "flex", alignItems: "flex-end", gap: 8, minHeight: 74 }}>
                  {M_SPARK.map((h, i) => (
                    <div key={i} style={{ flex: 1, display: "flex", alignItems: "flex-end", height: 74 }}>
                      <div style={{ width: "100%", height: `${h}%`, borderRadius: 4, background: "linear-gradient(180deg,#4f9dff,#34d3e0)" }} />
                    </div>
                  ))}
                </div>
                <div style={{ display: "flex", justifyContent: "space-between", font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                  <span>{t("audit.daysAgo6")}</span><span>{t("daily.today")}</span>
                </div>
              </div>
            </div>

            {/* session list */}
            <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "16px 18px", display: "flex", flexDirection: "column", gap: 11 }}>
              <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("audit.sessionHistory")}</span>
              {M_SESSIONS.map((s) => (
                <div key={s.name} style={{ display: "flex", alignItems: "center", gap: 12, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "10px 13px" }}>
                  <div style={{ display: "flex", flexDirection: "column", gap: 2, flex: 1, minWidth: 0 }}>
                    <span style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.name}</span>
                    <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{s.meta} · {s.when}</span>
                  </div>
                  <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "#34d3e0", width: 48, textAlign: "right" }}>{s.tok}</span>
                  <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)", width: 58, textAlign: "right" }}>{s.dur}</span>
                  <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "var(--green)", background: "var(--tint-green)", border: "1px solid var(--tint-green-bd)", padding: "2px 7px", borderRadius: 5 }}>done</span>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* ── OPTIMIZE (run diff) ──────────────────────────────── */}
        {tab === "optimize" && (
          <OptimizePanel
            summaries={runSummaries}
            live={live}
            baseRun={baseRun}
            candRun={candRun}
            setBaseRun={setBaseRun}
            setCandRun={setCandRun}
          />
        )}
      </div>

      {/* ── part drawer ─────────────────────────────────────────── */}
      {part && (
        <div
          onClick={() => setPart(null)}
          style={{ position: "absolute", inset: 0, background: "rgba(0,0,0,.5)", display: "flex", justifyContent: "flex-end", zIndex: 40 }}
        >
          <div
            onClick={(e) => e.stopPropagation()}
            style={{ width: partFull ? "100%" : 520, flex: "none", background: "var(--bg-panel)", borderLeft: "1px solid var(--bd)", display: "flex", flexDirection: "column", minHeight: 0 }}
          >
            <div style={{ flex: "none", padding: "15px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 11 }}>
              <span style={{ font: "600 8px 'IBM Plex Mono'", color: part.layer ? LAYER_COLOR[part.layer] : "var(--amber)", background: "var(--bg-inset2)", border: "1px solid var(--bd2)", padding: "2px 6px", borderRadius: 4, flex: "none" }}>{part.layer ?? part.ptype}</span>
              <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0, flex: 1 }}>
                <span style={{ font: "700 14px 'IBM Plex Mono'", color: "var(--tx)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{part.label}</span>
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{part.crumb}</span>
              </div>
              <div onClick={() => setPartFull((v) => !v)} style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer", font: "600 10px 'IBM Plex Sans'", color: "var(--tx2)", padding: "6px 12px", border: "1px solid var(--bd2)", borderRadius: 7, background: "var(--bg-card2)" }}>
                <span style={{ fontSize: 13 }}>{partFull ? "⤡" : "⤢"}</span>
                {t(partFull ? "audit.shrink" : "audit.fullscreen")}
              </div>
              <div onClick={() => setPart(null)} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 19px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
            </div>
            <div style={{ flex: "none", padding: "11px 20px", borderBottom: "1px solid var(--bd-soft)", display: "flex", alignItems: "center", gap: 16, flexWrap: "wrap" }}>
              {part.fields.map((f) => (
                <div key={f.k} style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{f.k}</span>
                  <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx3)" }}>{f.v}</span>
                </div>
              ))}
            </div>
            <div style={{ flex: 1, overflowY: "auto", padding: "20px 24px", background: "var(--bg-deep)" }}>
              {part.body.kind === "text" ? (
                <p style={{ font: "400 12.5px/1.9 'IBM Plex Sans'", color: "var(--tx2)", margin: 0, whiteSpace: "pre-wrap" }}>{part.body.content}</p>
              ) : (
                <pre style={{ font: "500 11.5px/1.85 'IBM Plex Mono'", color: part.body.kind === "json" ? "#9fe0c2" : "var(--tx3)", margin: 0, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{part.body.content}</pre>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/* ── optimize (run diff) ──────────────────────────────────────────── */

// `t` here is an i18n key, translated at the call site.
const VERDICT: Record<string, { t: string; c: string; bg: string; bd: string }> = {
  improved: { t: "audit.verdict.improved", c: "var(--green)", bg: "var(--tint-green)", bd: "var(--tint-green-bd)" },
  regressed: { t: "audit.verdict.regressed", c: "var(--red)", bg: "var(--tint-red)", bd: "var(--tint-red-bd)" },
  mixed: { t: "audit.verdict.mixed", c: "var(--amber)", bg: "var(--tint-amber)", bd: "var(--bd2)" },
  unchanged: { t: "audit.verdict.unchanged", c: "var(--tx-dim)", bg: "var(--bg-inset2)", bd: "var(--bd3)" },
};

function fmtMetric(label: string, n: number): string {
  if (label.includes("token")) return fmtTokens(n);
  if (label.includes("ms")) return `${n}ms`;
  return String(n);
}

function OptimizePanel({ summaries, live, baseRun, candRun, setBaseRun, setCandRun }: {
  summaries: RunSummary[];
  live: boolean;
  baseRun: string | null;
  candRun: string | null;
  setBaseRun: (r: string) => void;
  setCandRun: (r: string) => void;
}) {
  const { t } = useTranslation();
  if (summaries.length === 0) {
    return (
      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center" }}>
        <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
          {t(live ? "audit.noRunsYet" : "audit.noData")}
        </span>
      </div>
    );
  }

  // default: newest run is the candidate, next-newest the baseline
  const cand = summaries.find((s) => s.run === candRun) ?? summaries[0];
  const base = summaries.find((s) => s.run === baseRun && s.run !== cand.run)
    ?? summaries.find((s) => s.run !== cand.run)
    ?? summaries[0];
  const diff = base.run !== cand.run ? diffRuns(base, cand) : null;
  const v = diff ? VERDICT[diff.verdict] : VERDICT.unchanged;

  const stageNames = Array.from(new Set([...base.stages.map((s) => s.stage), ...cand.stages.map((s) => s.stage)]));
  const stageTok = (r: RunSummary, name: string) => r.stages.find((s) => s.stage === name)?.tokensEst ?? 0;
  const stageMax = Math.max(1, ...stageNames.flatMap((n) => [stageTok(base, n), stageTok(cand, n)]));

  const pill = (on: boolean, color: string): React.CSSProperties => ({
    font: "600 8.5px 'IBM Plex Mono'", cursor: "pointer", padding: "2px 7px", borderRadius: 5,
    color: on ? color : "var(--tx-dim)", background: on ? "var(--bg-inset2)" : "transparent",
    border: `1px solid ${on ? color : "var(--bd2)"}`,
  });

  return (
    <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
      {/* run list */}
      <div style={{ width: 288, flex: "none", borderRight: "1px solid var(--bd)", padding: "16px 12px", display: "flex", flexDirection: "column", gap: 9, overflowY: "auto" }}>
        <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("audit.runs")}</span>
        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("audit.pickBaseCand")}</span>
        {summaries.map((s) => {
          const isBase = s.run === base.run, isCand = s.run === cand.run;
          return (
            <div key={s.run} style={{ display: "flex", flexDirection: "column", gap: 6, padding: "10px 11px", borderRadius: 8, background: "var(--bg-card)", border: `1px solid ${isCand ? "var(--tint-active-bd)" : isBase ? "var(--bd2)" : "var(--bd)"}` }}>
              <span style={{ font: "600 11px 'IBM Plex Mono'", color: "var(--tx2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.run}</span>
              <div style={{ display: "flex", alignItems: "center", gap: 8, font: "500 9px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                <span>{s.calls} calls</span>
                <span style={{ color: "#34d3e0" }}>{fmtTokens(s.tokensEst)}</span>
                {s.errors > 0 && <span style={{ color: "var(--red)" }}>{s.errors} err</span>}
              </div>
              <div style={{ display: "flex", gap: 6 }}>
                <span onClick={() => setBaseRun(s.run)} style={pill(isBase, "var(--ac)")}>{t("audit.base")}</span>
                <span onClick={() => setCandRun(s.run)} style={pill(isCand, "#34d3e0")}>{t("audit.candidate")}</span>
              </div>
            </div>
          );
        })}
      </div>

      {/* diff */}
      <div style={{ flex: 1, overflowY: "auto", padding: "18px 22px", display: "flex", flexDirection: "column", gap: 16 }}>
        {!diff ? (
          <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("audit.pickTwoRuns")}</span>
        ) : (
          <>
            <div style={{ display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap" }}>
              <span style={{ font: "700 15px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("audit.runDiff")}</span>
              <span style={{ font: "600 10px 'IBM Plex Mono'", color: v.c, background: v.bg, border: `1px solid ${v.bd}`, padding: "3px 9px", borderRadius: 6 }}>{t(v.t)}</span>
              <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                <span style={{ color: "var(--ac)" }}>{base.run}</span> → <span style={{ color: "#34d3e0" }}>{cand.run}</span>
              </span>
            </div>

            {/* headline: token savings */}
            <div style={{ display: "flex", gap: 11, flexWrap: "wrap" }}>
              {(() => {
                const d0 = diff.deltas[0];
                const better = isBetter(d0);
                const c = d0.diff === 0 ? "var(--tx-dim)" : better ? "var(--green)" : "var(--red)";
                return (
                  <div style={{ flex: 1, minWidth: 200, background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 10, padding: "13px 16px", display: "flex", flexDirection: "column", gap: 4 }}>
                    <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("audit.totalTokenChange")}</span>
                    <span style={{ font: "700 22px 'IBM Plex Sans'", color: c, letterSpacing: "-0.5px" }}>
                      {d0.diff > 0 ? "+" : d0.diff < 0 ? "−" : "±"}{fmtTokens(Math.abs(d0.diff))}
                    </span>
                    <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                      {fmtTokens(base.tokensEst)} → {fmtTokens(cand.tokensEst)} {d0.pct !== null && `(${d0.pct > 0 ? "+" : ""}${d0.pct.toFixed(0)}%)`}
                    </span>
                  </div>
                );
              })()}
            </div>

            {/* delta table */}
            <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "14px 18px", display: "flex", flexDirection: "column", gap: 3 }}>
              <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px", marginBottom: 6 }}>{t("audit.metric")}</span>
              {diff.deltas.map((d: Delta) => {
                const better = isBetter(d);
                const c = d.diff === 0 ? "var(--tx-dim)" : better ? "var(--green)" : "var(--red)";
                return (
                  <div key={d.label} style={{ display: "flex", alignItems: "center", gap: 10, padding: "6px 0", borderBottom: "1px solid var(--bd-soft)" }}>
                    <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx3)", width: 130, flex: "none" }}>{d.label}</span>
                    <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)", width: 66, textAlign: "right" }}>{fmtMetric(d.label, d.base)}</span>
                    <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>→</span>
                    <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx2)", width: 66, textAlign: "right" }}>{fmtMetric(d.label, d.cand)}</span>
                    <span style={{ marginLeft: "auto", font: "600 10px 'IBM Plex Mono'", color: c }}>
                      {d.diff > 0 ? "+" : d.diff < 0 ? "−" : "±"}{fmtMetric(d.label, Math.abs(d.diff))}
                      {d.pct !== null && <span style={{ color: "var(--tx-faint)", fontWeight: 400 }}>  {d.pct > 0 ? "+" : ""}{d.pct.toFixed(0)}%</span>}
                    </span>
                  </div>
                );
              })}
            </div>

            {/* per-stage token comparison */}
            <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "14px 18px", display: "flex", flexDirection: "column", gap: 12 }}>
              <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("audit.tokensByStage")}</span>
              {stageNames.map((name) => {
                const b = stageTok(base, name), cv = stageTok(cand, name);
                return (
                  <div key={name} style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                    <div style={{ display: "flex", alignItems: "center", gap: 8, font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                      <span style={{ color: "var(--tx3)" }}>{name}</span>
                      <span style={{ marginLeft: "auto" }}>{fmtTokens(b)} → <span style={{ color: cv <= b ? "var(--green)" : "var(--red)" }}>{fmtTokens(cv)}</span></span>
                    </div>
                    <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
                      <div style={{ height: 7, background: "var(--bd3)", borderRadius: 4, overflow: "hidden" }}>
                        <div style={{ width: `${(b / stageMax) * 100}%`, height: "100%", background: "var(--ac)" }} />
                      </div>
                      <div style={{ height: 7, background: "var(--bd3)", borderRadius: 4, overflow: "hidden" }}>
                        <div style={{ width: `${(cv / stageMax) * 100}%`, height: "100%", background: "#34d3e0" }} />
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
