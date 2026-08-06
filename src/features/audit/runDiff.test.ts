import { describe, it, expect } from "vitest";
import type { AccessLog } from "@/lib/gateway";
import { summarizeRuns, diffRuns, isBetter } from "./runDiff";

function log(over: Partial<AccessLog>): AccessLog {
  return {
    time: "2026-07-12T10:00:00.000Z",
    requestId: "req",
    session: "sess-1",
    service: "anthropic",
    method: "POST",
    path: "/anthropic/v1/messages",
    status: 200,
    reqBytes: 0,
    respBytes: 0,
    durationMs: 1000,
    ...over,
  };
}

const withTool = JSON.stringify({ content: [{ type: "tool_use", id: "t", name: "write_file", input: {} }, { type: "text", text: "ok" }] });

describe("summarizeRuns", () => {
  it("aggregates tokens, tool round-trips, errors and stages per run", () => {
    const runs = summarizeRuns([
      log({ requestId: "a", run: "R1", stage: "plan", tokensEst: 1000, inputTokens: 800, outputTokens: 200, respBody: withTool, time: "2026-07-12T10:00:00.000Z" }),
      log({ requestId: "b", run: "R1", stage: "code", tokensEst: 3000, inputTokens: 2000, outputTokens: 1000, respBody: withTool, status: 500, time: "2026-07-12T10:02:00.000Z" }),
    ]);
    expect(runs).toHaveLength(1);
    const r = runs[0];
    expect(r.run).toBe("R1");
    expect(r.calls).toBe(2);
    expect(r.tokensEst).toBe(4000);
    expect(r.inputTokens).toBe(2800);
    expect(r.toolCalls).toBe(2);
    expect(r.errors).toBe(1);
    expect(r.stages.map((s) => s.stage)).toEqual(["plan", "code"]);
    expect(r.wallMs).toBe(120000); // 2 minutes
  });

  it("returns runs most-recent first", () => {
    const runs = summarizeRuns([
      log({ run: "old", time: "2026-07-12T08:00:00.000Z" }),
      log({ run: "new", time: "2026-07-12T10:00:00.000Z" }),
    ]);
    expect(runs.map((r) => r.run)).toEqual(["new", "old"]);
  });
});

describe("diffRuns", () => {
  const base = summarizeRuns([log({ run: "v1", tokensEst: 3000, respBody: withTool, status: 500 })])[0];
  const cand = summarizeRuns([log({ run: "v2", tokensEst: 2000, respBody: withTool })])[0];

  it("marks fewer tokens as improved and computes a signed percentage", () => {
    const d = diffRuns(base, cand);
    expect(d.verdict).toBe("improved");
    const tokens = d.deltas[0];
    expect(tokens.diff).toBe(-1000);
    expect(tokens.pct).toBeCloseTo(-33.33, 1);
    expect(isBetter(tokens)).toBe(true);
  });

  it("flags mixed when tokens drop but errors rise", () => {
    const worseErrors = summarizeRuns([log({ run: "v3", tokensEst: 2000, respBody: withTool, status: 500 }), log({ run: "v3", tokensEst: 0, status: 400, time: "2026-07-12T10:05:00.000Z" })])[0];
    const d = diffRuns(base, worseErrors);
    expect(d.verdict).toBe("mixed");
  });
});
