import { describe, it, expect } from "vitest";
import { fmtTokens, parseStageLog, runSteps, stepTotals, type Step } from "./steps";
import type { RunStatus } from "@/lib/sandbox";

const line = (o: unknown) => JSON.stringify(o);

describe("parseStageLog", () => {
  it("reads the agent's JSON log lines and ignores anything else", () => {
    const log = [
      "warning: something on stderr",
      line({ type: "task_start", model: "claude-sonnet-5" }),
      line({ type: "turn", iteration: 1, toolCalls: ["read_file"], usage: { inputTokens: 900, outputTokens: 120 } }),
      line({ type: "tool_result", tool: "read_file" }),
      line({ type: "turn", iteration: 2, stopReason: "end_turn", usage: { inputTokens: 1000, outputTokens: 200 } }),
      line({ type: "task_done", iteration: 2 }),
      "not json at all",
    ].join("\n");

    const steps = parseStageLog(log);
    expect(steps.map((s) => s.kind)).toEqual(["turn", "tool", "turn"]);
    const first = steps[0] as Extract<Step, { kind: "turn" }>;
    expect(first.tools).toEqual(["read_file"]);
    expect(first.tokens).toBe(1020);
  });

  // A model round that called nothing and ended nothing is noise in a list whose
  // value is being short enough to read.
  it("drops turns that did nothing observable", () => {
    const log = [
      line({ type: "turn", iteration: 1, stopReason: "tool_use", toolCalls: [] }),
      line({ type: "turn", iteration: 2, stopReason: "end_turn" }),
    ].join("\n");
    expect(parseStageLog(log).map((s) => s.kind)).toEqual(["turn"]);
  });

  it("marks a failed tool as failed", () => {
    const steps = parseStageLog(line({ type: "tool_result", tool: "write_file", isError: true }));
    expect(steps[0]).toEqual({ kind: "tool", name: "write_file", error: true });
  });

  it("reports in-stage compaction as a saving and a failed one as an error", () => {
    const ok = parseStageLog(line({ type: "compaction", before: 40, after: 8, tokens: 130000 }));
    expect(ok[0]).toEqual({ kind: "compaction", before: 40, after: 8, tokens: 130000 });

    // The same type with no counts means summarizing failed — the operator wants
    // to know, and it is not a saving.
    const bad = parseStageLog(line({ type: "compaction", message: "summarize failed: 500" }));
    expect(bad[0].kind).toBe("error");
  });

  it("surfaces a truncated answer, which otherwise just reads as unfinished", () => {
    const steps = parseStageLog(line({ type: "task_stopped", stopReason: "max_tokens", message: "response hit max_tokens; stopping" }));
    expect(steps[0]).toEqual({ kind: "error", message: "response hit max_tokens; stopping" });
  });

  it("handles an empty or absent log without inventing steps", () => {
    expect(parseStageLog("")).toEqual([]);
    expect(parseStageLog(undefined as unknown as string)).toEqual([]);
  });
});

describe("runSteps", () => {
  const status = (ids: string[]): RunStatus => ({
    id: "r1",
    taskId: "t",
    status: "done",
    maxParallel: 1,
    stages: ids.map((id) => ({
      id, name: id.toUpperCase(), role: "impl", dependsOn: [], containerId: "c",
      status: "done" as const, exitCode: 0,
    })),
  });

  // Labelling the single stage of a Solo run with its own name repeats the
  // message's byline and says nothing.
  it("adds stage headers only when there is more than one stage", () => {
    const solo = runSteps(status(["a"]), { a: line({ type: "tool_result", tool: "read_file" }) });
    expect(solo.map((s) => s.kind)).toEqual(["tool"]);

    // …except when the stage's log yielded nothing. A command stage runs a shell
    // rather than the agent loop, so it emits no JSON lines; without its header
    // the work log would be empty and the row offering it would disappear on the
    // click that opened it.
    const command = runSteps(status(["build"]), { build: "+ npm ci\nadded 412 packages\n" });
    expect(command.map((s) => s.kind)).toEqual(["stage"]);
    expect(command[0]).toMatchObject({ kind: "stage", name: "BUILD", status: "done" });

    const team = runSteps(status(["a", "b"]), {
      a: line({ type: "tool_result", tool: "read_file" }),
      b: line({ type: "tool_result", tool: "write_file" }),
    });
    expect(team.map((s) => s.kind)).toEqual(["stage", "tool", "stage", "tool"]);
  });

  it("is empty before a run exists", () => {
    expect(runSteps(null, {})).toEqual([]);
  });
});

describe("stepTotals", () => {
  it("counts tool calls, tokens and failures for the collapsed header", () => {
    const steps: Step[] = [
      { kind: "turn", n: 1, tools: ["a"], tokens: 1200, stop: "tool_use" },
      { kind: "tool", name: "a", error: false },
      { kind: "tool", name: "b", error: true },
      { kind: "error", message: "boom" },
    ];
    expect(stepTotals(steps)).toEqual({ tools: 2, tokens: 1200, errors: 2, searches: 0 });
  });

  // Searches are counted apart from tool calls because they are billed apart
  // from tokens: nothing else in the header reflects them.
  it("counts provider-side searches on their own", () => {
    const steps: Step[] = [
      { kind: "search", action: "search", query: "backoff", used: 1, limit: 5, images: 0 },
      { kind: "search", action: "open_page", query: "https://go.dev", used: 2, limit: 5, images: 0 },
      { kind: "tool", name: "read_file", error: false },
    ];
    const totals = stepTotals(steps);
    expect(totals.searches).toBe(2);
    expect(totals.tools).toBe(1);
  });
});

describe("web search steps", () => {
  it("reads a search, its query and how much of the grant is spent", () => {
    const steps = parseStageLog(line({
      type: "web_search", iteration: 1, tool: "search",
      message: "exponential backoff", count: 2, limit: 5,
    }));
    expect(steps[0]).toEqual({ kind: "search", action: "search", query: "exponential backoff", used: 2, limit: 5, images: 0 });
  });

  // One OpenAI grant covers searching, opening a page and reading it; the action
  // is kept so the log can say which happened.
  it("keeps the action a search actually took", () => {
    const steps = parseStageLog(line({
      type: "web_search", tool: "open_page", message: "https://go.dev/blog/retry", count: 3, limit: 5,
    }));
    expect(steps[0]).toMatchObject({ kind: "search", action: "open_page" });
  });

  // A search that brought pictures back says so: the retrieval happened on the
  // provider's side, so no gateway saw it and this is the only record of it.
  it("carries how many images a search returned", () => {
    const steps = parseStageLog(line({
      type: "web_search", tool: "search", message: "conceptual diagram", count: 1, limit: 5, images: 3,
    }));
    expect(steps[0]).toMatchObject({ kind: "search", images: 3 });
  });

  it("reports a spent grant as its own step", () => {
    const steps = parseStageLog(line({ type: "web_search_exhausted", count: 5, limit: 5 }));
    expect(steps[0]).toEqual({ kind: "searchLimit", used: 5, limit: 5 });
  });
});

describe("fmtTokens", () => {
  it("stays short", () => {
    expect(fmtTokens(0)).toBe("0");
    expect(fmtTokens(940)).toBe("940");
    expect(fmtTokens(1400)).toBe("1.4K");
    expect(fmtTokens(2_500_000)).toBe("2.50M");
  });
});
