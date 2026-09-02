// Shared agent-template domain: types, default catalog, and pure helpers.
//
// This is the single source of truth consumed by the settings UI (AgentsPanel),
// the persisted store (useStore), and the run compiler (compileTemplate). A
// "Graph template" is a DAG of nodes (each bound to a Solo agent) plus edges;
// the orchestrator that runs it only ever sees the compiled Stage[] — so graphs
// and orchestration stay decoupled and freely configurable.

import type { MediaTools, WebSearch } from "@/lib/sandbox";
import type { ProviderInput } from "@/lib/providers";
// Importing i18n here (not just in main.tsx) is what guarantees it is
// initialised before the defaults below are read.
import i18n from "@/i18n";

/** A single-role agent definition (the atom templates are built from). */
export interface SoloAgent {
  id: string;
  name: string;
  role: string;
  /**
   * What this atom actually is.
   *
   * "agent" (the default, and what every Solo was before this field existed)
   * runs the LLM tool-use loop. "command" runs a fixed command in a chosen
   * sandbox image and no model is involved at all — that is how a template gets
   * a build, a test run or a video transcode into its DAG.
   *
   * The distinction lives here, on the atom, rather than as a separate node
   * type: Graph nodes and Supervisor workers both bind to a Solo, so adding it
   * here is what makes `plan (agent) → build (command) → review (agent)`
   * expressible in every template shape at once.
   *
   * It is deliberately NOT "give the agent a shell": the model still cannot
   * execute anything. A command stage is authored here, by a human, and its
   * command is fixed before the run starts.
   */
  kind?: "agent" | "command";
  /**
   * Sandbox image policy name ("base" | "poly" | "media" | a custom one from
   * Settings → サンドボックス). Only meaningful for `kind: "command"`; agent
   * stages always run the default image. Undefined => the controller default.
   */
  image?: string;
  /**
   * The shell command a `kind: "command"` atom runs, e.g.
   * `npm ci && npm test`. Compiled to `bash -lc "<cmd>"`, so the image must
   * carry a shell — `base` is distroless and deliberately does not.
   * The task text is available to it as $ORCHESTRA_TASK.
   */
  cmd?: string;
  /** The provider (gateway connection) this agent runs on. */
  providerId: string;
  /** A model id offered by that provider. */
  model: string;
  /** Reasoning effort (cost lever): low|medium|high|xhigh|max. Undefined => the
   * agent's default (medium). Lower = cheaper/faster, less deliberation. */
  effort?: string;
  /** Per-response output-token cap. Undefined => the agent default (16000). */
  maxTokens?: number;
  ctx: string;
  strat: string;
  dot: string;
  arch: string;
  /** Role system prompt. Falls back to archExamples[arch] when unset. */
  system?: string;
  /** Ids of custom tools this agent may use (opt-in; default none). */
  toolIds?: string[];
  /** When true, the agent gets a rag_search tool (knowledge base via gateway). */
  useRag?: boolean;
  /** Which media this agent may generate. Each enabled kind becomes one tool;
   *  leaving a kind off means the tool does not exist for this agent, which is
   *  the difference between "cannot" and "asked not to". Video is the expensive
   *  one and is deliberately its own switch. */
  media?: MediaTools;
  /** How many knowledge relations this agent may follow out of the task's
   *  scope. 0 (the default) keeps the scope exactly as chosen; 1 adds the
   *  groups a scoped group directly requires, and so on.
   *
   *  A relation was documentation — "this one requires that one" — and reading
   *  it as a grant is only safe because of this bound: without it, a single
   *  edge drawn on the canvas could connect every group in the graph. */
  /** When set, this agent gets a web_search tool. The search itself runs on the
   *  model provider's side — the sandbox gets no route to the web — but it is
   *  still an explicit grant: searches cost money per use, and an agent that was
   *  not given the tool answers from what it already knows rather than
   *  pretending to have looked. Anthropic-dialect agents only. */
  web?: WebSearch;
}

/** Default provider catalog (mirrors configs/gateway.json model providers).
 * Seeds the store; the gateway is the runtime source of truth after sync. */
export const defaultProviders: ProviderInput[] = [
  {
    name: "anthropic", kind: "model", dialect: "anthropic", prefix: "/anthropic/", upstream: "https://api.anthropic.com",
    allowlist: ["api.anthropic.com"],
    models: ["claude-opus-4-8", "claude-sonnet-5", "claude-haiku-4-5-20251001", "claude-fable-5"],
    injectHeaders: { "x-api-key": "${SECRET}", "anthropic-version": "2023-06-01" },
  },
  {
    name: "openai", kind: "model", dialect: "openai", prefix: "/openai/", upstream: "https://api.openai.com",
    allowlist: ["api.openai.com"],
    models: ["gpt-4o", "gpt-4o-mini", "o3", "o4-mini"],
    injectHeaders: { Authorization: "Bearer ${SECRET}" },
  },
  {
    name: "gemini", kind: "model", dialect: "gemini", prefix: "/gemini/", upstream: "https://generativelanguage.googleapis.com",
    allowlist: ["generativelanguage.googleapis.com"],
    models: ["gemini-2.5-pro", "gemini-2.5-flash"],
    injectHeaders: { "x-goog-api-key": "${SECRET}" },
  },
  {
    // Tool provider for Delivery's GitHub issue pull. Set a fine-grained PAT
    // (Issues: Read) via "鍵設定"; the gateway injects it and blocks writes.
    name: "github", kind: "tool", prefix: "/github/", upstream: "https://api.github.com",
    allowlist: ["api.github.com"],
    models: [],
    injectHeaders: { Authorization: "Bearer ${SECRET}", Accept: "application/vnd.github+json" },
  },
];

/** One node of a graph template. Bound to a Solo — or (recursive composition)
 * to another template via templateId, which is expanded into a sub-DAG. */
export interface GraphNode {
  id: string;
  soloId?: string;
  templateId?: string;
  model?: string;
  effort?: string;
  maxTokens?: number;
  system?: string;
  task?: string;
}

/** A configurable DAG of role agents. edges are [fromNodeId, toNodeId]. */
export interface GraphTemplate {
  id: string;
  name: string;
  desc: string;
  pattern: "graph";
  nodes: GraphNode[];
  edges: [string, string][];
}

/** A central supervisor delegating to workers (persist/edit only for now). */
export interface SupervisorTemplate {
  id: string;
  name: string;
  desc: string;
  pattern: "supervisor";
  supervisor: string; // soloId
  workers: string[]; // soloIds
}

export type StaticTemplate = GraphTemplate | SupervisorTemplate;

/* ─────────────────────────── defaults ─────────────────────────── */

// The shipped catalog is language-dependent: a role label is UI chrome, but a
// role's system prompt is text a model reads, and both should follow the
// language the operator chose. So these are functions of the active locale
// rather than frozen constants — call them, do not capture them at import.
//
// Ids never move. Only the human-readable name/role/desc and the prompts do,
// which is what lets the store tell an untouched default apart from an edited
// one across a language switch (see reseedTemplates in useStore).

const T = (k: string, lng?: string) => i18n.t(k, { lng }) as string;

export function defaultSolos(lng?: string): SoloAgent[] {
  return [
    { id: "planner", name: "Planner", role: T("templates.roles.plan", lng), providerId: "anthropic", model: "claude-opus-4-8", ctx: "200k", strat: T("templates.strat.ragOn", lng), dot: "#4f9dff", arch: "plan" },
    { id: "builder", name: "Builder", role: T("templates.roles.build", lng), providerId: "anthropic", model: "claude-sonnet-5", ctx: "128k", strat: T("templates.strat.compressStandard", lng), dot: "#34d3e0", arch: "build" },
    { id: "reviewer", name: "Reviewer", role: T("templates.roles.review", lng), providerId: "anthropic", model: "claude-opus-4-8", ctx: "200k", strat: T("templates.strat.ragStrict", lng), dot: "#b08ad9", arch: "review" },
    { id: "tester", name: "Tester", role: T("templates.roles.test", lng), providerId: "anthropic", model: "claude-haiku-4-5-20251001", ctx: "64k", strat: T("templates.strat.compressAggressive", lng), dot: "#3fbf8f", arch: "test" },
    { id: "researcher", name: "Researcher", role: T("templates.roles.research", lng), providerId: "anthropic", model: "claude-sonnet-5", ctx: "128k", strat: T("templates.strat.webOn", lng), dot: "#e0a83e", arch: "research", web: { maxUses: 5 } },
  ];
}

/** Build a linear graph template whose nodes are the given solos in order. */
function chain(id: string, name: string, desc: string, soloIds: string[]): GraphTemplate {
  const nodes: GraphNode[] = soloIds.map((s) => ({ id: s, soloId: s }));
  const edges: [string, string][] = [];
  for (let i = 1; i < soloIds.length; i++) edges.push([soloIds[i - 1], soloIds[i]]);
  return { id, name, desc, pattern: "graph", nodes, edges };
}

export function defaultStaticTpls(lng?: string): StaticTemplate[] {
  return [
    chain("impl", T("templates.impl.name", lng), T("templates.impl.desc", lng), ["planner", "builder", "tester", "reviewer"]),
    chain("research", T("templates.research.name", lng), T("templates.research.desc", lng), ["researcher", "planner", "builder"]),
    { id: "review", name: T("templates.review.name", lng), desc: T("templates.review.desc", lng), pattern: "supervisor", supervisor: "reviewer", workers: ["builder", "tester"] },
  ];
}

export function archChips(lng?: string) {
  return [
    { id: "plan", label: T("templates.roles.plan", lng) },
    { id: "build", label: T("templates.roles.build", lng) },
    { id: "review", label: T("templates.roles.review", lng) },
    { id: "test", label: T("templates.roles.test", lng) },
    { id: "research", label: T("templates.roles.research", lng) },
    { id: "generic", label: T("templates.roles.generic", lng) },
  ];
}

/** The stock role system prompt for an arch, in the given (or active) locale. */
export function archExample(arch: string, lng?: string): string {
  const known = ["plan", "build", "review", "test", "research", "generic"];
  return T(`prompts.roles.${known.includes(arch) ? arch : "generic"}`, lng);
}

export function defaultGlobalPrompt(lng?: string): string {
  return T("prompts.global", lng);
}

export function defaultDynamicPrompt(lng?: string): string {
  return T("prompts.dynamic", lng);
}

/* ─────────────────────────── helpers ─────────────────────────── */

/** Resolve a solo's effective role system prompt. */
export function soloSystem(solo: SoloAgent): string {
  return solo.system ?? archExample(solo.arch);
}
