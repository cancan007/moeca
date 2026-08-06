// runDiff.ts — the "optimize" loop: summarize each orchestration run from the
// gateway access log, then diff two runs so a change (a shorter prompt, context
// compression, a different model, fewer tool round-trips) can be measured
// instead of eyeballed.
//
// A run is identified by its ORCHESTRA run id (AccessLog.run); calls with no run
// are grouped under a synthetic "solo" bucket per session. Pure + testable.

import type { AccessLog } from "@/lib/gateway";

export interface StageSummary {
  stage: string;
  calls: number;
  tokensEst: number;
}

export interface RunSummary {
  run: string;              // run id (or "session:xxxx" for solo)
  calls: number;
  tokensEst: number;
  inputTokens: number;
  outputTokens: number;
  toolCalls: number;        // number of gateway calls that are tool/tool_use round-trips
  errors: number;
  wallMs: number;           // last.time - first.time
  avgDurationMs: number;
  models: string[];
  stages: StageSummary[];
  firstTs: string;
  lastTs: string;
}

function runKeyOf(l: AccessLog): string {
  return l.run || `session:${(l.session || "unknown").slice(0, 8)}`;
}

// countToolUses estimates tool_use blocks in a captured response (best-effort;
// the capture may be truncated, so this is a lower bound).
function countToolUses(respBody?: string): number {
  if (!respBody) return 0;
  try {
    const obj = JSON.parse(respBody) as { content?: { type?: string }[] };
    if (Array.isArray(obj.content)) return obj.content.filter((b) => b.type === "tool_use").length;
  } catch {
    // truncated capture: fall back to a marker count
    return (respBody.match(/"type"\s*:\s*"tool_use"/g) || []).length;
  }
  return 0;
}

export function summarizeRuns(logs: AccessLog[]): RunSummary[] {
  const order: string[] = [];
  const byRun = new Map<string, AccessLog[]>();
  for (const l of logs) {
    const k = runKeyOf(l);
    if (!byRun.has(k)) { byRun.set(k, []); order.push(k); }
    byRun.get(k)!.push(l);
  }

  const summaries = order.map((k) => {
    const calls = [...byRun.get(k)!].sort((a, b) => a.time.localeCompare(b.time));
    const tokensEst = sum(calls, (l) => l.tokensEst ?? 0);
    const inputTokens = sum(calls, (l) => l.inputTokens ?? 0);
    const outputTokens = sum(calls, (l) => l.outputTokens ?? 0);
    const toolCalls = sum(calls, (l) => countToolUses(l.respBody));
    const errors = calls.filter((l) => l.status === 0 || l.status >= 400).length;
    const first = calls[0], last = calls[calls.length - 1];
    const wallMs = Math.max(0, new Date(last.time).getTime() - new Date(first.time).getTime());
    const avgDurationMs = calls.length ? Math.round(sum(calls, (l) => l.durationMs) / calls.length) : 0;

    // per-stage
    const stageOrder: string[] = [];
    const byStage = new Map<string, AccessLog[]>();
    for (const l of calls) {
      const sk = l.stage || "main";
      if (!byStage.has(sk)) { byStage.set(sk, []); stageOrder.push(sk); }
      byStage.get(sk)!.push(l);
    }
    const stages: StageSummary[] = stageOrder.map((sk) => ({
      stage: sk,
      calls: byStage.get(sk)!.length,
      tokensEst: sum(byStage.get(sk)!, (l) => l.tokensEst ?? 0),
    }));

    return {
      run: k, calls: calls.length, tokensEst, inputTokens, outputTokens, toolCalls, errors,
      wallMs, avgDurationMs, models: uniq(calls.map((l) => l.model).filter(Boolean) as string[]),
      stages, firstTs: first.time, lastTs: last.time,
    };
  });

  // most recent runs first
  return summaries.sort((a, b) => b.lastTs.localeCompare(a.lastTs));
}

export interface Delta {
  label: string;
  base: number;
  cand: number;
  diff: number;      // cand - base
  pct: number | null; // null when base is 0
  // for these metrics, lower is better (fewer tokens / calls / ms / errors)
  lowerIsBetter: boolean;
}

export interface RunDiff {
  base: RunSummary;
  cand: RunSummary;
  deltas: Delta[];
  verdict: "improved" | "regressed" | "mixed" | "unchanged";
}

function delta(label: string, base: number, cand: number, lowerIsBetter = true): Delta {
  const diff = cand - base;
  const pct = base === 0 ? null : (diff / base) * 100;
  return { label, base, cand, diff, pct, lowerIsBetter };
}

// diffRuns compares a baseline run against a candidate. Verdict is driven by the
// headline cost metric (total tokens): fewer tokens for the same work = improved.
export function diffRuns(base: RunSummary, cand: RunSummary): RunDiff {
  const deltas: Delta[] = [
    delta("total tokens", base.tokensEst, cand.tokensEst),
    delta("input tokens", base.inputTokens, cand.inputTokens),
    delta("output tokens", base.outputTokens, cand.outputTokens),
    delta("LLM calls", base.calls, cand.calls),
    delta("tool round-trips", base.toolCalls, cand.toolCalls),
    delta("errors", base.errors, cand.errors),
    delta("wall time (ms)", base.wallMs, cand.wallMs),
    delta("avg latency (ms)", base.avgDurationMs, cand.avgDurationMs),
  ];

  // verdict from the headline metric first, corroborated by call count
  const token = deltas[0];
  let verdict: RunDiff["verdict"];
  if (token.diff < 0) verdict = "improved";
  else if (token.diff > 0) verdict = "regressed";
  else verdict = "unchanged";
  // mixed: tokens improved but errors got worse (or vice versa)
  const errD = deltas[5];
  if (verdict === "improved" && errD.diff > 0) verdict = "mixed";
  if (verdict === "regressed" && errD.diff < 0) verdict = "mixed";

  return { base, cand, deltas, verdict };
}

// isBetter reports whether a delta moved in the good direction.
export function isBetter(d: Delta): boolean {
  if (d.diff === 0) return false;
  return d.lowerIsBetter ? d.diff < 0 : d.diff > 0;
}

function sum<T>(xs: T[], f: (x: T) => number): number {
  return xs.reduce((s, x) => s + f(x), 0);
}
function uniq<T>(xs: T[]): T[] {
  return Array.from(new Set(xs));
}
