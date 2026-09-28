import { describe, it, expect } from "vitest";
import {
  AUTO_MARGIN,
  buildHistory,
  buildTask,
  contextState,
  defaultHistorySettings,
  deriveTitle,
  estimateTokens,
  liveTokens,
  parseCtxBudget,
  replaceableCount,
  ringDash,
  stageCompaction,
  summaryBudget,
  type HistorySettings,
  type TurnLike,
} from "./chatContext";

const turn = (kind: TurnLike["kind"], text: string, dropped = false): TurnLike =>
  ({ kind, text, tokensEst: estimateTokens(text), dropped });

/** n question/answer pairs, each costing `tokens` tokens per message. */
const exchanges = (n: number, tokens = 100): TurnLike[] =>
  Array.from({ length: n }, (_, i) => [
    { kind: "user" as const, text: `q${i}`, tokensEst: tokens, dropped: false },
    { kind: "agent" as const, text: `a${i}`, tokensEst: tokens, dropped: false },
  ]).flat();

const settings = (over: Partial<HistorySettings> = {}): HistorySettings => ({ ...defaultHistorySettings, ...over });

describe("parseCtxBudget", () => {
  it("reads the shapes an operator actually types", () => {
    expect(parseCtxBudget("200k")).toBe(200_000);
    expect(parseCtxBudget("128K")).toBe(128_000);
    expect(parseCtxBudget("1M")).toBe(1_000_000);
    expect(parseCtxBudget("64000")).toBe(64_000);
    expect(parseCtxBudget(" 1.5k ")).toBe(1_500);
  });

  // A budget of nothing would make every conversation look instantly full, so
  // an unusable value falls back rather than reading as zero.
  it("falls back instead of reporting no budget at all", () => {
    expect(parseCtxBudget("")).toBe(128_000);
    expect(parseCtxBudget(undefined)).toBe(128_000);
    expect(parseCtxBudget("たくさん")).toBe(128_000);
    expect(parseCtxBudget("0")).toBe(128_000);
  });
});

describe("estimateTokens", () => {
  it("counts code points, not bytes", () => {
    // Four Japanese characters are four code points; counting bytes would make
    // this twelve and a Japanese transcript would look three times its size.
    expect(estimateTokens("あいうえおかきくけこ")).toBe(2);
    expect(estimateTokens("abcdefgh")).toBe(2);
    expect(estimateTokens("")).toBe(0);
  });
});

describe("liveTokens", () => {
  it("does not charge for what was dropped", () => {
    const turns = [turn("user", "a".repeat(400), true), turn("agent", "b".repeat(40))];
    expect(liveTokens(turns)).toBe(10);
  });
});

describe("replaceableCount", () => {
  it("keeps whole exchanges and cuts on a question", () => {
    const turns = exchanges(5);
    // Keeping 2 exchanges leaves the last four messages: the cut is at index 6.
    expect(replaceableCount(turns, 2)).toBe(6);
    expect(turns[6].kind).toBe("user");
  });

  it("offers nothing when there is less history than we promised to keep", () => {
    expect(replaceableCount(exchanges(2), 4)).toBe(0);
    expect(replaceableCount([], 4)).toBe(0);
  });

  it("counts only what is still being sent", () => {
    const turns = [...exchanges(3).map((t) => ({ ...t, dropped: true })), ...exchanges(3)];
    // The dropped half is not eligible twice.
    expect(replaceableCount(turns, 2)).toBe(2);
  });
});

describe("contextState", () => {
  const agent = { ctx: "10k" } as const;

  it("suggests compaction once the declared budget is mostly spent", () => {
    // 40 messages × 200 tokens = 8000 of a 10k budget = 80%.
    const st = contextState(exchanges(20, 200), agent, settings({ thresholdPct: 70 }));
    expect(st.used).toBe(8_000);
    expect(st.ratio).toBeCloseTo(0.8);
    expect(st.suggest).toBe(true);
    expect(st.auto).toBe(false);
  });

  it("stops asking and acts once there is no room left to ask in", () => {
    const st = contextState(exchanges(24, 200), agent, settings({ thresholdPct: 70 }));
    expect(st.ratio).toBeCloseTo(0.96);
    expect(st.auto).toBe(true);
    expect(settings().thresholdPct + AUTO_MARGIN).toBe(85);
  });

  it("says nothing at any fill level when the operator asked to keep everything", () => {
    const full = contextState(exchanges(30, 200), agent, settings({ strategy: "full" }));
    expect(full.suggest).toBe(false);
    expect(full.auto).toBe(false);
    // The ring still reports the cost — the setting suppresses the offer to fix
    // it, not the fact that it is being paid.
    expect(full.ratio).toBe(1);

    const off = contextState(exchanges(30, 200), agent, settings({ on: false }));
    expect(off.suggest).toBe(false);
    expect(off.used).toBeGreaterThan(0);
  });

  it("does not offer a compaction that would replace nothing", () => {
    // Over threshold, but only two exchanges exist and four are promised.
    const st = contextState(exchanges(2, 4000), agent, settings({ keepTurns: 4 }));
    expect(st.ratio).toBeGreaterThan(0.7);
    expect(st.replaceable).toBe(0);
    expect(st.suggest).toBe(false);
  });
});

describe("summaryBudget", () => {
  it("is proportional between a floor and a cap", () => {
    expect(summaryBudget(120)).toBe(256);
    expect(summaryBudget(12_000)).toBe(1_000);
    expect(summaryBudget(10_000_000)).toBe(2_048);
  });
});

describe("ringDash", () => {
  it("fills nothing at empty and everything at full", () => {
    const circumference = 2 * Math.PI * 10.5;
    const [filled] = ringDash(0).split(" ").map(Number);
    expect(filled).toBe(0);
    const [full] = ringDash(1).split(" ").map(Number);
    expect(full).toBeCloseTo(circumference, 1);
    // Over-full is clamped rather than drawing a second lap.
    expect(ringDash(3)).toBe(ringDash(1));
  });
});

describe("buildHistory", () => {
  it("omits dropped turns and marks a briefing as one", () => {
    const history = buildHistory([
      turn("user", "old question", true),
      turn("summary", "they want retries"),
      turn("user", "and now?"),
      turn("agent", "here you go"),
    ]);
    expect(history).not.toContain("old question");
    expect(history).toContain("[Summary of the conversation so far]");
    expect(history).toContain("person: and now?");
    expect(history).toContain("assistant: here you go");
  });

  it("skips turns with nothing in them", () => {
    // A turn still running has no text yet; it must not appear as an empty line
    // the model has to interpret.
    expect(buildHistory([turn("agent", "   "), turn("user", "hi")])).toBe("person: hi");
  });
});

describe("buildTask", () => {
  it("keeps quoted material distinguishable from the message", () => {
    const task = buildTask("person: earlier", ["the third paragraph"], "explain this");
    expect(task).toContain("# Conversation so far");
    expect(task).toContain("> the third paragraph");
    expect(task).toContain("# Message\n\nexplain this");
    // The quote is introduced as context, so an agent does not answer it as if
    // it were the instruction.
    expect(task).toContain("not an instruction");
  });

  it("leaves out sections it has nothing for", () => {
    const task = buildTask("", [], "first message");
    expect(task).not.toContain("# Conversation so far");
    expect(task).not.toContain("# Quoted");
    expect(task).toContain("first message");
  });
});

describe("deriveTitle", () => {
  it("takes the first sentence when it fits", () => {
    expect(deriveTitle("リトライ間隔を指数バックオフにしたい。理由は…")).toBe("リトライ間隔を指数バックオフにしたい");
    expect(deriveTitle("Why is the index rebuild so slow? It takes 40 minutes."))
      .toBe("Why is the index rebuild so slow");
  });

  it("clips a long opening rather than running the sentence on", () => {
    const title = deriveTitle("この設計のトレードオフについて、前回の議論を踏まえたうえでできるだけ詳しく説明してください");
    expect([...title].length).toBe(41); // 40 plus the ellipsis
    expect(title.endsWith("…")).toBe(true);
  });

  it("collapses newlines so the name is one line", () => {
    expect(deriveTitle("決済のリトライ\n\nについて相談")).toBe("決済のリトライ について相談");
  });

  it("drops markers and code fences that are not a name", () => {
    expect(deriveTitle("- 決済のリトライ")).toBe("決済のリトライ");
    expect(deriveTitle("## 決済のリトライ")).toBe("決済のリトライ");
    expect(deriveTitle("```\nnpm ci\n```\nこれが失敗します")).toBe("これが失敗します");
  });

  // Nothing quotable is not a title made of punctuation: the UI has a word for
  // an unnamed conversation and should keep using it.
  it("gives nothing back for a message with nothing in it", () => {
    expect(deriveTitle("   ")).toBe("");
    expect(deriveTitle("")).toBe("");
    expect(deriveTitle("```\nnpm ci\n```")).toBe("");
  });

  it("does not cut on a decimal point or an early terminator", () => {
    // A terminator at position 0 would otherwise produce an empty name.
    expect(deriveTitle("...とりあえず動かしたい")).toBe("...とりあえず動かしたい");
  });
});

describe("stageCompaction", () => {
  it("summarizes at the same fraction of the same budget the chat ring uses", () => {
    expect(stageCompaction({ ctx: "200k" }, settings({ thresholdPct: 70 }))).toEqual({ maxContext: 140_000 });
    expect(stageCompaction({ ctx: "64k" }, settings({ thresholdPct: 50 }))).toEqual({ maxContext: 32_000 });
  });

  // "Off" has to survive being sent as JSON: 0 is what an absent field decodes
  // to, so disabling compaction is expressed as a negative.
  it("disables compaction explicitly rather than falling back to the default", () => {
    expect(stageCompaction({ ctx: "200k" }, settings({ on: false })).maxContext).toBeLessThan(0);
    expect(stageCompaction({ ctx: "200k" }, settings({ strategy: "full" })).maxContext).toBeLessThan(0);
  });

  it("never compiles a budget too small to hold a single turn", () => {
    expect(stageCompaction({ ctx: "1k" }, settings({ thresholdPct: 10 })).maxContext).toBe(4_000);
  });
});
