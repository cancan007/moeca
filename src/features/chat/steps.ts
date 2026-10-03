// What an agent did to produce one chat reply, read out of the run's own logs.
//
// The design shows a turn's work as a collapsible list under the answer, which is
// the right shape for it: a conversation is about the answer, and the fact that
// getting there took four tool calls and a compaction is available but not in the
// way. This module turns the agent runtime's log lines into that list.
//
// The agent emits one JSON object per line (agent/internal/agent/log.go) — that
// is the interface here, not the prose. Lines that are not JSON are ignored
// rather than shown: a container's stderr is not a step.
//
// It is deliberately structured rather than pre-formatted: a step says what it
// IS, and the component decides what words to draw it with, so the same parser
// serves three locales and the tests do not have to boot i18n to assert on it.

import type { RunStatus, StageStatus } from "@/lib/sandbox";

export type Step =
  /** A stage boundary — only interesting when a template has more than one. */
  | { kind: "stage"; name: string; role: string; status: StageStatus }
  /** One model turn: which tools it asked for, what it cost, why it stopped. */
  | { kind: "turn"; n: number; tools: string[]; tokens: number; stop: string }
  /** One tool the agent actually ran. */
  | { kind: "tool"; name: string; error: boolean }
  /** The agent summarized its own history mid-run (the in-stage layer). */
  | { kind: "compaction"; before: number; after: number; tokens: number }
  /** One provider-side web search. The agent does not run this tool — the model
   *  provider does — so counting what came back is the only account of it there
   *  is, and each one is billed separately from the tokens. */
  | { kind: "search"; action: string; query: string; used: number; limit: number; images: number }
  /** The search grant was spent and the tool withdrawn for the rest of the run. */
  | { kind: "searchLimit"; used: number; limit: number }
  /** What the stage published for whatever came after it. */
  | { kind: "handoff"; files: string[] }
  | { kind: "error"; message: string };

/** The subset of a log line this module reads. Mirrors logLine in the agent
 *  runtime; unknown fields are ignored so adding one there cannot break this. */
interface RawLine {
  type?: string;
  iteration?: number;
  tool?: string;
  toolCalls?: string[];
  isError?: boolean;
  stopReason?: string;
  message?: string;
  model?: string;
  files?: string[];
  before?: number;
  after?: number;
  tokens?: number;
  count?: number;
  limit?: number;
  images?: number;
  usage?: { inputTokens?: number; outputTokens?: number; input_tokens?: number; output_tokens?: number };
}

function usageTokens(u: RawLine["usage"]): number {
  if (!u) return 0;
  const inTok = u.inputTokens ?? u.input_tokens ?? 0;
  const outTok = u.outputTokens ?? u.output_tokens ?? 0;
  return inTok + outTok;
}

/** Parse one stage's container log into steps.
 *
 *  `turn` lines that neither called a tool nor ended the turn are dropped: an
 *  intermediate model round that did nothing observable is noise in a list whose
 *  whole value is being short enough to read. */
export function parseStageLog(log: string): Step[] {
  const out: Step[] = [];
  for (const line of (log ?? "").split("\n")) {
    const s = line.trim();
    if (!s.startsWith("{")) continue;
    let l: RawLine;
    try {
      l = JSON.parse(s) as RawLine;
    } catch {
      continue;
    }
    switch (l.type) {
      case "turn": {
        const tools = l.toolCalls ?? [];
        const stop = l.stopReason ?? "";
        if (tools.length === 0 && stop !== "end_turn" && stop !== "max_tokens") break;
        out.push({ kind: "turn", n: l.iteration ?? 0, tools, tokens: usageTokens(l.usage), stop });
        break;
      }
      case "tool_result":
        out.push({ kind: "tool", name: l.tool ?? "", error: !!l.isError });
        break;
      case "compaction":
        // A failed summarization also logs as "compaction", with a message and no
        // counts. It is an error the operator should see, not a saving.
        if (l.before || l.after) {
          out.push({ kind: "compaction", before: l.before ?? 0, after: l.after ?? 0, tokens: l.tokens ?? 0 });
        } else if (l.message) {
          out.push({ kind: "error", message: l.message });
        }
        break;
      case "web_search":
        out.push({
          kind: "search",
          action: l.tool || "search",
          query: l.message ?? "",
          used: l.count ?? 0,
          limit: l.limit ?? 0,
          images: l.images ?? 0,
        });
        break;
      case "web_search_exhausted":
        out.push({ kind: "searchLimit", used: l.count ?? 0, limit: l.limit ?? 0 });
        break;
      case "handoff":
        if ((l.files ?? []).length > 0) out.push({ kind: "handoff", files: l.files ?? [] });
        break;
      case "error":
        out.push({ kind: "error", message: l.message ?? "" });
        break;
      case "task_stopped":
        // Not an error, but the reply is cut short and the reason is the only
        // explanation of why it reads as unfinished.
        if (l.message) out.push({ kind: "error", message: l.message });
        break;
      default:
        break; // task_start / task_done add nothing the surrounding UI lacks
    }
  }
  return out;
}

/** Every step of a run, in stage order.
 *
 *  A stage header is emitted when the run has more than one stage — labelling the
 *  single stage of a Solo run with its own name says nothing the message's byline
 *  does not — and also whenever a stage's log yielded nothing to show. The second
 *  case is not cosmetic: a command stage runs a shell, not the agent loop, so it
 *  emits no JSON lines at all, and without its header the whole work log would
 *  come back empty and the row offering it would vanish on the click that opened
 *  it. A stage that ran and said nothing is still something that ran. */
export function runSteps(status: RunStatus | null, logs: Record<string, string>): Step[] {
  if (!status) return [];
  const multi = status.stages.length > 1;
  const out: Step[] = [];
  for (const st of status.stages) {
    const parsed = parseStageLog(logs[st.id] ?? "");
    if (multi || parsed.length === 0) {
      out.push({ kind: "stage", name: st.name || st.id, role: st.role, status: st.status });
    }
    out.push(...parsed);
  }
  return out;
}

/** Totals for the collapsed header: how many tool calls were made and what the
 *  turn cost. Tokens are summed over model turns, which is where the money is. */
export function stepTotals(steps: Step[]): { tools: number; tokens: number; errors: number; searches: number } {
  let tools = 0;
  let tokens = 0;
  let errors = 0;
  // Counted apart from tools because it is billed apart from tokens: a web
  // search is charged per use, and the whole point of surfacing it is that no
  // other number in this view reflects it.
  let searches = 0;
  for (const s of steps) {
    if (s.kind === "tool") {
      tools++;
      if (s.error) errors++;
    }
    if (s.kind === "turn") tokens += s.tokens;
    if (s.kind === "error") errors++;
    if (s.kind === "search") searches++;
  }
  return { tools, tokens, errors, searches };
}

/** Compact token count for the log header ("1.4K"). */
export function fmtTokens(n: number): string {
  if (!n) return "0";
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}
