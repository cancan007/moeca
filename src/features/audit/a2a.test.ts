import { describe, it, expect, beforeAll } from "vitest";
import type { AccessLog } from "@/lib/gateway";
import i18n from "@/i18n";
import { parseResponseBlocks, parseRequestInput, buildLiveTree } from "./a2a";

// A few labels this file asserts on are rendered through i18n, so the language
// is pinned rather than left to detection.
beforeAll(async () => {
  await i18n.changeLanguage("ja");
});

function log(over: Partial<AccessLog>): AccessLog {
  return {
    time: "2026-07-12T10:00:00.000Z",
    requestId: "req-1",
    session: "sess-abcdef12",
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

const RESP = JSON.stringify({
  content: [
    { type: "thinking", thinking: "I should batch the docs to bound memory." },
    { type: "tool_use", id: "tu_1", name: "write_file", input: { path: "indexer.ts", content: "…" } },
    { type: "text", text: "Done — indexer now batches in 500s." },
  ],
  usage: { input_tokens: 1200, output_tokens: 300 },
});

const REQ = JSON.stringify({
  model: "claude-opus-4-8",
  system: "You are a coding agent scoped to /work.",
  messages: [
    { role: "user", content: [{ type: "text", text: "rebuild the index in batches" }] },
    { role: "assistant", content: [{ type: "tool_use", id: "tu_0", name: "list_files", input: {} }] },
    { role: "user", content: [{ type: "tool_result", tool_use_id: "tu_0", content: "indexer.ts\ndb.ts" }] },
  ],
});

describe("parseResponseBlocks", () => {
  it("splits a response into thinking / tool_use / text layers in order", () => {
    const parts = parseResponseBlocks(RESP);
    expect(parts.map((p) => p.layer)).toEqual(["thinking", "tool_use", "text"]);
    expect(parts[0].body.content).toContain("batch");
    expect(parts[1].label).toBe("write_file");
    expect(parts[1].body.kind).toBe("json");
    expect(parts[2].body.content).toContain("Done");
  });

  it("surfaces a truncated (non-JSON) capture as a single raw part, dropping nothing", () => {
    const parts = parseResponseBlocks('{"content":[{"type":"thinking","thinking":"half of a str');
    expect(parts).toHaveLength(1);
    expect(parts[0].layer).toBe("raw");
    expect(parts[0].fields.some((f) => f.v.includes("truncated"))).toBe(true);
  });

  it("returns nothing for an empty body", () => {
    expect(parseResponseBlocks(undefined)).toEqual([]);
    expect(parseResponseBlocks("")).toEqual([]);
  });
});

describe("parseRequestInput", () => {
  it("extracts the system prompt and the newest turn (a tool_result) as input parts", () => {
    const parts = parseRequestInput(REQ);
    expect(parts[0].layer).toBe("system");
    expect(parts[0].body.content).toContain("coding agent");
    // newest message is the tool_result turn
    const input = parts.filter((p) => p.layer === "input");
    expect(input).toHaveLength(1);
    expect(input[0].label).toContain("tool_result");
    expect(input[0].body.content).toContain("indexer.ts");
  });

  it("handles a plain string message content", () => {
    const parts = parseRequestInput(JSON.stringify({ messages: [{ role: "user", content: "hello" }] }));
    expect(parts).toHaveLength(1);
    expect(parts[0].layer).toBe("input");
    expect(parts[0].body.content).toBe("hello");
  });
});

describe("buildLiveTree", () => {
  it("groups calls into Context(run) → Task(stage) → Message(call) → Part(block)", () => {
    const nodes = buildLiveTree([
      log({ requestId: "r1", run: "run-A", stage: "plan", model: "claude-opus-4-8", tokensEst: 1500, reqBody: REQ, respBody: RESP, time: "2026-07-12T10:00:00.000Z" }),
      log({ requestId: "r2", run: "run-A", stage: "code", model: "claude-opus-4-8", tokensEst: 2500, respBody: RESP, time: "2026-07-12T10:01:00.000Z" }),
    ]);

    const ctx = nodes.filter((n) => n.kind === "Context");
    const tasks = nodes.filter((n) => n.kind === "Task");
    const msgs = nodes.filter((n) => n.kind === "Message");
    const parts = nodes.filter((n) => n.kind === "Part");

    expect(ctx).toHaveLength(1);
    expect(ctx[0].label).toBe("run-A");
    expect(tasks.map((t) => t.label)).toEqual(["plan", "code"]);
    expect(msgs).toHaveLength(2);

    // depth + ancestry are well-formed
    expect(ctx[0].depth).toBe(0);
    expect(tasks[0].depth).toBe(1);
    expect(msgs[0].depth).toBe(2);
    expect(parts[0].depth).toBe(3);
    expect(parts[0].ancestors).toEqual([ctx[0].id, tasks[0].id, msgs[0].id]);

    // context tokens = sum of its calls
    expect(ctx[0].fields.find((f) => f.k === "tokensEst")?.v).toBe("4.0K");
  });

  it("groups run-less (solo) calls by session", () => {
    const nodes = buildLiveTree([log({ requestId: "s1", session: "sess-9999aaaa", respBody: RESP })]);
    const ctx = nodes.find((n) => n.kind === "Context");
    expect(ctx?.label).toBe("solo / ad-hoc");
    expect(ctx?.sub).toContain("session:");
  });
});

/* ── Artifact nodes ─────────────────────────────────────────────────── */

import type { RunStatus } from "@/lib/sandbox";

const logFor = (run: string, stage: string): AccessLog => ({
  time: "2026-07-27T00:00:00Z",
  requestId: `req-${run}-${stage}`,
  session: "dev",
  run,
  stage,
  service: "anthropic",
  model: "claude-opus-4-8",
  method: "POST",
  path: "/anthropic/v1/messages",
  upstream: "api.anthropic.com",
  status: 200,
  reqBytes: 0,
  respBytes: 0,
} as AccessLog);

const runWith = (stages: RunStatus["stages"]): RunStatus =>
  ({ id: "run-1", taskId: "t", status: "done", maxParallel: 1, stages }) as RunStatus;

describe("buildLiveTree artifacts", () => {
  // Artifacts are filesystem effects, so they cannot come from the gateway log.
  it("emits nothing without run status", () => {
    const nodes = buildLiveTree([logFor("run-1", "builder")]);
    expect(nodes.some((n) => n.kind === "Artifact")).toBe(false);
  });

  it("attaches a stage's produced files under its Task", () => {
    const runs = new Map([["run-1", runWith([
      { id: "builder", name: "Builder", role: "実装", dependsOn: [], containerId: "c", status: "done", exitCode: 0,
        commit: "abcdef1234567890", parent: "0123456789abcdef",
        files: [{ path: "src/a.ts", additions: 10, deletions: 2 }, { path: "src/b.ts", additions: 1, deletions: 0 }] },
    ])]]);

    const nodes = buildLiveTree([logFor("run-1", "builder")], runs);
    const art = nodes.find((n) => n.kind === "Artifact");
    expect(art).toBeDefined();
    expect(art!.ancestors).toEqual(["ctx:run-1", "task:run-1:builder"]);
    expect(art!.label).toBe("2 ファイル");
    expect(art!.sub).toBe("abcdef12 · +11 −2");
    expect(art!.parts?.map((p) => p.label)).toEqual(["src/a.ts", "src/b.ts"]);
  });

  it("names the file directly when a stage touched exactly one", () => {
    const runs = new Map([["run-1", runWith([
      { id: "builder", name: "B", role: "r", dependsOn: [], containerId: "c", status: "done", exitCode: 0,
        commit: "deadbeefcafe", files: [{ path: "README.md", additions: 3, deletions: 1 }] },
    ])]]);
    expect(buildLiveTree([logFor("run-1", "builder")], runs).find((n) => n.kind === "Artifact")!.label).toBe("README.md");
  });

  // A read-only stage is a normal outcome, not an empty artifact.
  it("emits nothing for a stage that changed nothing", () => {
    const runs = new Map([["run-1", runWith([
      { id: "planner", name: "P", role: "計画", dependsOn: [], containerId: "c", status: "done", exitCode: 0 },
    ])]]);
    expect(buildLiveTree([logFor("run-1", "planner")], runs).some((n) => n.kind === "Artifact")).toBe(false);
  });

  // git reports binary files as "-"; the client sends -1. Those must not be
  // summed into a total that reads as real line counts.
  it("does not count binary files as lines", () => {
    const runs = new Map([["run-1", runWith([
      { id: "builder", name: "B", role: "r", dependsOn: [], containerId: "c", status: "done", exitCode: 0,
        commit: "abc123def456", files: [{ path: "logo.png", additions: -1, deletions: -1 }] },
    ])]]);
    const art = buildLiveTree([logFor("run-1", "builder")], runs).find((n) => n.kind === "Artifact")!;
    expect(art.sub).toBe("abc123de · +0 −0");
    expect(art.parts![0].body.content).toContain("(binary)");
  });
});
