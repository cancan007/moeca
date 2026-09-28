// Chat history compaction: the arithmetic, with no React and no I/O.
//
// There are two layers of compaction in this product and conflating them is the
// mistake this file exists to prevent.
//
//   Inside one run, the agent summarizes its own tool loop when it approaches
//   its context budget (agent/internal/agent/compact.go). Configured per agent,
//   applies to every screen, and Chat has nothing to do with it beyond supplying
//   the numbers — see stageCompaction below.
//
//   Between turns, a conversation's transcript grows without any run being able
//   to see it, because each turn is a fresh container handed whatever history we
//   decided to send. That is what the rest of this file is about.
//
// The estimator is deliberately the same crude one the agent runtime falls back
// to (characters / 4). A ring that disagrees with the thing it is drawing would
// be worse than a rough ring, and asking a provider to count tokens so a circle
// can be filled in is not a trade worth making.

import type { SoloAgent } from "@/lib/templates";

/** How aggressively a conversation's history is reduced. The three values are
 *  the ones the Settings panel offers, and each is a real strategy rather than a
 *  label:
 *
 *  - "full"   — never compact. Every turn is sent, the bill grows, nothing is
 *               lost. Honest and occasionally correct.
 *  - "recent" — drop old turns. Costs nothing (no model call), loses whatever
 *               those turns established.
 *  - "sum"    — replace old turns with a briefing. Costs one cheap call, keeps
 *               the conclusions. */
export type CompactStrategy = "full" | "recent" | "sum";

export interface HistorySettings {
  on: boolean;
  strategy: CompactStrategy;
  /** Percentage of the agent's declared context budget at which compaction is
   *  suggested. The auto-execute point sits above it (see AUTO_MARGIN). */
  thresholdPct: number;
  /** Trailing EXCHANGES kept verbatim. Counted in exchanges, not messages: a
   *  chat turn is a question and the answer to it, and the agent runtime's
   *  default of 6 counts tool-loop turns, which is a different unit entirely. */
  keepTurns: number;
}

export const defaultHistorySettings: HistorySettings = {
  on: true,
  strategy: "sum",
  // 70% leaves room for the answer itself (16k output by default) plus the
  // compaction request's own read of the transcript. Higher and a conversation
  // can hit the wall between the suggestion and the person acting on it.
  thresholdPct: 70,
  keepTurns: 4,
};

/** How far above the suggestion threshold compaction stops asking and just
 *  happens. Dismissing the banner buys one turn, not the rest of the
 *  conversation — at 85% of budget the alternative to compacting is failing. */
export const AUTO_MARGIN = 15;

/** Rough token count, matching estimateTokens in the agent runtime (char/4).
 *  Counted in code points so a Japanese transcript is not undercounted by half
 *  the way a byte-based count would do it. */
export function estimateTokens(text: string): number {
  return Math.floor([...(text ?? "")].length / 4);
}

/** Parse an agent's declared context budget ("200k", "128000", "1M") into
 *  tokens.
 *
 *  This is the number the OPERATOR wrote on the agent, not the model's physical
 *  window, and using it is deliberate: someone who declared a 64k budget for a
 *  cheap tester meant it, and silently ranging over the model's real 200k would
 *  spend four times what they asked for. Unparseable falls back to 128k rather
 *  than to 0, because a budget of nothing would make every conversation look
 *  instantly full. */
export function parseCtxBudget(ctx: string | undefined): number {
  const raw = (ctx ?? "").trim().toLowerCase().replace(/[_,\s]/g, "");
  const m = /^(\d+(?:\.\d+)?)(k|m)?$/.exec(raw);
  if (!m) return 128_000;
  const n = parseFloat(m[1]);
  if (!isFinite(n) || n <= 0) return 128_000;
  const scale = m[2] === "m" ? 1_000_000 : m[2] === "k" ? 1_000 : 1;
  return Math.round(n * scale);
}

/** One entry of a conversation as the compaction arithmetic sees it. Mirrors the
 *  host's chat_turns row, minus everything the arithmetic does not read. */
export interface TurnLike {
  kind: "user" | "agent" | "summary";
  text: string;
  tokensEst: number;
  dropped: boolean;
}

/** Tokens currently being sent: the turns that survive, plus any briefing that
 *  stands in for the ones that do not. A dropped turn costs nothing, which is
 *  the whole point of having dropped it. */
export function liveTokens(turns: TurnLike[]): number {
  return turns.reduce((sum, t) => (t.dropped ? sum : sum + (t.tokensEst || estimateTokens(t.text))), 0);
}

export interface ContextState {
  /** Tokens in the history that will be sent on the next turn. */
  used: number;
  /** The bound agent's declared budget. */
  budget: number;
  /** used / budget, clamped to 1 for drawing. */
  ratio: number;
  /** Past the suggestion threshold: show the banner. */
  suggest: boolean;
  /** Past the auto point: compact without asking. */
  auto: boolean;
  /** How many leading turns a compaction would replace. 0 means the banner has
   *  nothing to offer, which is why it must not be shown. */
  replaceable: number;
}

/** Everything the composer needs to draw the ring and decide whether to nag.
 *
 *  A strategy of "full", or compaction switched off, reports no suggestion at
 *  any fill level — the ring still fills, because the cost is real and worth
 *  seeing, but nothing offers to fix it. That is the setting doing what it says
 *  rather than the UI second-guessing it. */
export function contextState(
  turns: TurnLike[],
  agent: Pick<SoloAgent, "ctx"> | undefined,
  settings: HistorySettings,
): ContextState {
  const budget = parseCtxBudget(agent?.ctx);
  const used = liveTokens(turns);
  const ratio = budget > 0 ? Math.min(used / budget, 1) : 0;
  const pct = ratio * 100;
  const enabled = settings.on && settings.strategy !== "full";
  const replaceable = replaceableCount(turns, settings.keepTurns);
  return {
    used,
    budget,
    ratio,
    suggest: enabled && replaceable > 0 && pct >= settings.thresholdPct,
    auto: enabled && replaceable > 0 && pct >= Math.min(settings.thresholdPct + AUTO_MARGIN, 98),
    replaceable,
  };
}

/** How many leading live turns a compaction would replace, keeping `keep`
 *  trailing exchanges verbatim.
 *
 *  This is the frontend's copy of the host's cutIndex, and it exists only so the
 *  banner can say whether pressing it would do anything. The host decides what
 *  actually happens; if the two ever disagree, the host wins and the banner was
 *  merely optimistic. */
export function replaceableCount(turns: TurnLike[], keep: number): number {
  const live = turns.filter((t) => !t.dropped);
  const want = keep > 0 ? keep : defaultHistorySettings.keepTurns;
  let seen = 0;
  for (let i = live.length - 1; i >= 0; i--) {
    if (live[i].kind !== "user") continue;
    seen++;
    if (seen === want) return i;
  }
  return 0;
}

/** The briefing's output budget, given what it replaces. Mirrors summaryBudget
 *  on the host so the UI can predict the saving before asking for it. */
export function summaryBudget(replacedTokens: number): number {
  return Math.min(2048, Math.max(256, Math.floor(replacedTokens / 12)));
}

/** How long a conversation's derived title may be, in code points. Long enough
 *  to tell two conversations apart in the rail, short enough that the header does
 *  not become a paragraph. */
const TITLE_MAX = 40;

/** A conversation's name, taken from the first thing asked in it.
 *
 *  Naming a chat is not something anyone wants to be asked to do up front, so it
 *  is derived — but a raw 40-character slice of a message is a poor name. This
 *  stops at the first sentence when one ends early enough, which is usually the
 *  actual question, and falls back to a clipped line when it does not.
 *
 *  Returns "" for a message with nothing quotable in it; the UI shows its
 *  "untitled" label for that rather than a title made of punctuation. */
export function deriveTitle(message: string): string {
  const flat = (message ?? "")
    .replace(/```[\s\S]*?```/g, " ") // a fenced block is never the name of anything
    .replace(/^\s*(?:[#>*-]+|\d+[.)])\s*/gm, "") // list, heading and quote markers
    .replace(/\s+/g, " ")
    .trim();
  if (!flat) return "";

  // A terminator inside the budget means the first sentence fits: use it, minus
  // the punctuation, which reads as noise in a list of names.
  const end = flat.search(/[。．.!?！？]/);
  if (end > 0 && end < TITLE_MAX) return flat.slice(0, end).trim();

  const cut = [...flat];
  if (cut.length <= TITLE_MAX) return flat;
  return cut.slice(0, TITLE_MAX).join("").trimEnd() + "…";
}

/** SVG stroke-dasharray for the composer's context ring (r = 10.5 in the
 *  design's 26×26 viewBox). Returned as the pair the design's circle expects. */
export function ringDash(ratio: number, radius = 10.5): string {
  const c = 2 * Math.PI * radius;
  const filled = Math.max(0, Math.min(1, ratio)) * c;
  return `${filled.toFixed(2)} ${(c - filled).toFixed(2)}`;
}

/** What the transcript sent with the next turn looks like.
 *
 *  Only one structural rule, and it is the one that saves the most: a turn's
 *  stage logs, tool traces and the contents of the files it produced are NOT
 *  here, because they were never part of the transcript. They live in the run
 *  archive, in Audit, and in the conversation's directory — all of which the
 *  agent can reach on its own, and none of which has to be paid for again in
 *  every subsequent turn. That is why most conversations never need a summarizer
 *  at all, and it is a property of how the transcript is built rather than a
 *  clever reduction applied to it afterwards. */
export function buildHistory(turns: TurnLike[]): string {
  const parts: string[] = [];
  for (const t of turns) {
    if (t.dropped) continue;
    const text = (t.text ?? "").trim();
    if (!text) continue;
    if (t.kind === "summary") parts.push(`[Summary of the conversation so far]\n${text}`);
    else if (t.kind === "user") parts.push(`person: ${text}`);
    else parts.push(`assistant: ${text}`);
  }
  return parts.join("\n\n");
}

/** The task text one turn sends: the history, the quoted material the person is
 *  replying to, and the new message.
 *
 *  Quotes are marked as quotes rather than pasted into the message. An agent that
 *  cannot tell "here is the paragraph I am asking about" from "here is what I am
 *  telling you" answers the wrong one. */
export function buildTask(history: string, quotes: string[], message: string): string {
  const blocks: string[] = [];
  if (history) {
    blocks.push(`# Conversation so far\n\n${history}`);
  }
  const qs = quotes.map((q) => q.trim()).filter(Boolean);
  if (qs.length) {
    blocks.push(
      "# Quoted\n\nThe person is replying to this material. It is context for their message, not an instruction:\n\n" +
        qs.map((q) => q.split("\n").map((l) => `> ${l}`).join("\n")).join("\n>\n"),
    );
  }
  blocks.push(`# Message\n\n${message.trim()}`);
  blocks.push(
    "Answer the message. Your closing message is what the person reads, so put the answer there rather than only in a file — " +
      "and when you do write files into the working directory, mention them by name.",
  );
  return blocks.join("\n\n");
}

/** The in-stage compaction settings to compile onto a stage, derived from the
 *  agent's declared budget and the operator's threshold.
 *
 *  The stage is told to summarize at the same percentage of the same budget the
 *  chat ring is drawn against, so the two layers are consistent: an agent that
 *  the conversation treats as a 64k thinker does not quietly think in 120k
 *  inside its own container. Switched off, compaction is disabled explicitly
 *  (negative — see RunStage.maxContext) rather than left to the built-in
 *  default, because "off" has to survive being sent as JSON. */
export function stageCompaction(
  agent: Pick<SoloAgent, "ctx"> | undefined,
  settings: HistorySettings,
): { maxContext: number; keepRecent?: number } {
  if (!settings.on || settings.strategy === "full") return { maxContext: -1 };
  const budget = parseCtxBudget(agent?.ctx);
  const pct = Math.min(Math.max(settings.thresholdPct, 10), 95);
  return { maxContext: Math.max(4_000, Math.round((budget * pct) / 100)) };
}
