// Compiles a Graph template (DAG of role nodes) into the orchestrator's Stage[].
// This is the seam that keeps templates and orchestration decoupled: templates
// are authored freely in the UI, and this pure function lowers them to the
// generic Stage DAG the backend executes. Pure and side-effect-free by design.

import type { RunStage } from "@/lib/sandbox";
import type { ProviderInput } from "@/lib/providers";
import { type ToolDef, type CompiledTool, compileTool, RAG_SEARCH_TOOL } from "@/lib/tools";
import { type GraphTemplate, type SupervisorTemplate, type StaticTemplate, type SoloAgent, soloSystem } from "@/lib/templates";
// The orchestration prompts this file appends are read from the active locale.
// Still pure with respect to its arguments — the language is ambient config,
// the same way the model catalog is.
import i18n from "@/i18n";

/** Context shared by all compilers: the pools stages resolve against. */
export interface CompileCtx {
  solos: SoloAgent[];
  providers: ProviderInput[];
  tools: ToolDef[];
  templates: StaticTemplate[]; // for template-nodes (recursive composition)
  task: string;
}

/** Compile a single Solo into a one-stage run (used by scheduled Solo runs). */
export function compileSolo(
  solo: SoloAgent | undefined,
  providers: ProviderInput[],
  tools: ToolDef[],
  task: string,
): RunStage[] {
  if (!solo) return [];
  const provider = providers.find((p) => p.name === solo.providerId);
  const stage = stageFromSolo({ solos: [], providers, tools, templates: [], task }, solo.id, solo, provider, []);
  return stage ? [stage] : [];
}

/** Build one stage from a Solo, resolving provider (dialect+route), model,
 * system prompt, custom tools and the optional rag_search tool.
 *
 * A `kind: "command"` Solo takes the branch below instead: it needs no provider
 * and no model, because no model runs. That is also why the provider check that
 * follows must not apply to it — a command atom with no provider configured is
 * perfectly valid, and dropping it would silently remove a build or transcode
 * step from the middle of a DAG. */
function stageFromSolo(
  ctx: CompileCtx,
  id: string,
  solo: SoloAgent | undefined,
  provider: ProviderInput | undefined,
  dependsOn: string[],
  overrides?: { system?: string; model?: string; task?: string; effort?: string; maxTokens?: number },
): RunStage | null {
  if (solo?.kind === "command") return commandStage(ctx, id, solo, dependsOn, overrides?.task);
  if (!solo || !provider) return null;
  const toolById = new Map(ctx.tools.map((t) => [t.id, t]));
  const customTools = (solo.toolIds ?? []).map((tid) => toolById.get(tid)).filter((t): t is ToolDef => !!t).map(compileTool);
  const tools: CompiledTool[] = solo.useRag ? [...customTools, RAG_SEARCH_TOOL] : customTools;
  return {
    id,
    name: solo.name,
    role: solo.role,
    model: overrides?.model ?? solo.model,
    effort: overrides?.effort ?? solo.effort,
    maxTokens: overrides?.maxTokens ?? solo.maxTokens,
    provider: provider.dialect || "anthropic",
    providerPrefix: provider.prefix,
    system: overrides?.system ?? soloSystem(solo),
    task: overrides?.task ?? ctx.task,
    dependsOn,
    // Generation is in here too: image/speech/video are artifact tools now, so
    // a stage carries one list of tools rather than a list plus a media block
    // that only one vendor's API shape fitted.
    tools,
    // Web search only exists in the Anthropic dialect. Compiling the grant onto
    // an OpenAI or Gemini stage would put a tool in the run spec that neither
    // side executes, so the stage is compiled honestly: no grant, and the agent
    // answers from what it knows instead of appearing to have searched.
    web: solo.web && (provider.dialect || "anthropic") === "anthropic" ? solo.web : undefined,
  };
}

/**
 * Build a stage that runs a command instead of an agent.
 *
 * The model fields are emptied rather than faked: the orchestrator only sets
 * ORCHESTRA_MODEL / _PROVIDER / _BASE_URL when they are non-empty, so a command
 * stage reaches its container carrying no model configuration and no session
 * routing it has no use for. What it does get is the task text, so a command can
 * read `$ORCHESTRA_TASK` and act on what the schedule actually asked for.
 *
 * The image is a POLICY NAME, not a reference — the sandbox controller resolves
 * it against its allowlist and supplies the ref, network posture, caps and
 * scratch mounts. A template therefore cannot describe how a container runs,
 * only which of the approved shapes it wants.
 */
function commandStage(ctx: CompileCtx, id: string, solo: SoloAgent, dependsOn: string[], taskOverride?: string): RunStage | null {
  const cmd = (solo.cmd ?? "").trim();
  if (!cmd) return null;
  return {
    id,
    name: solo.name,
    role: solo.role,
    model: "",
    provider: "",
    providerPrefix: "",
    system: "",
    task: taskOverride ?? ctx.task,
    dependsOn,
    tools: [],
    image: solo.image || undefined,
    // `-l` so the image's profile drop-ins (which put the toolchains on PATH)
    // are sourced; without it `go` is not found in the polyglot image.
    cmd: ["bash", "-lc", cmd],
  };
}

// Topological order of node ids over edges [from,to] (graph is a DAG).
function topoOrder(nodeIds: string[], edges: [string, string][]): string[] {
  const indeg = new Map(nodeIds.map((n) => [n, 0]));
  const adj = new Map<string, string[]>();
  for (const [from, to] of edges) {
    if (!indeg.has(from) || !indeg.has(to)) continue;
    indeg.set(to, (indeg.get(to) ?? 0) + 1);
    adj.set(from, [...(adj.get(from) ?? []), to]);
  }
  const q = nodeIds.filter((n) => (indeg.get(n) ?? 0) === 0);
  const order: string[] = [];
  while (q.length) {
    const n = q.shift()!;
    order.push(n);
    for (const m of adj.get(n) ?? []) {
      indeg.set(m, (indeg.get(m) ?? 0) - 1);
      if ((indeg.get(m) ?? 0) === 0) q.push(m);
    }
  }
  // Any nodes left (cycle) are appended so nothing is silently dropped.
  for (const n of nodeIds) if (!order.includes(n)) order.push(n);
  return order;
}

/** Expansion result: the produced stages, and the stage ids downstream should
 * depend on (the unit's "exit"). */
interface Expansion {
  stages: RunStage[];
  exitIds: string[];
}

// resolve a solo id to {solo, provider}.
function resolveSolo(ctx: CompileCtx, soloId: string | undefined) {
  const solo = ctx.solos.find((s) => s.id === soloId);
  return { solo, provider: solo && ctx.providers.find((p) => p.name === solo.providerId) };
}

/**
 * Expand a graph into a flat Stage DAG. A node bound to a Solo becomes one
 * stage; a node bound to another Template is recursively expanded into a
 * sub-DAG and wired in (its source stages inherit the node's incoming deps; its
 * sink stages become the node's exit). Ids are namespaced by `prefix` so nested
 * stages never collide. `visited` guards against a template containing itself.
 */
function expandGraph(graph: GraphTemplate, ctx: CompileCtx, prefix: string, externalDeps: string[], visited: Set<string>): Expansion {
  const nodeIds = graph.nodes.map((n) => n.id);
  const idSet = new Set(nodeIds);
  const withinDeps = (nid: string) => graph.edges.filter(([, to]) => to === nid).map(([f]) => f).filter((f) => idSet.has(f));
  const exitByNode = new Map<string, string[]>();
  const stages: RunStage[] = [];

  for (const nid of topoOrder(nodeIds, graph.edges)) {
    const node = graph.nodes.find((n) => n.id === nid)!;
    const deps = withinDeps(nid).flatMap((d) => exitByNode.get(d) ?? []);
    const incoming = withinDeps(nid).length === 0 ? [...deps, ...externalDeps] : deps;
    const r = expandNode(node, ctx, prefix, incoming, visited);
    stages.push(...r.stages);
    exitByNode.set(nid, r.exitIds);
  }

  const sinks = graph.nodes.filter((n) => !graph.edges.some(([from]) => from === n.id)).map((n) => n.id);
  const exitIds = sinks.flatMap((n) => exitByNode.get(n) ?? []);
  return { stages, exitIds };
}

function expandNode(
  node: { id: string; soloId?: string; templateId?: string; system?: string; model?: string; effort?: string; maxTokens?: number; task?: string },
  ctx: CompileCtx,
  prefix: string,
  incoming: string[],
  visited: Set<string>,
): Expansion {
  // Template node → recursively expand the referenced template.
  if (node.templateId) {
    if (visited.has(node.templateId)) return { stages: [], exitIds: incoming }; // cycle: bridge deps through
    const tpl = ctx.templates.find((t) => t.id === node.templateId);
    if (!tpl) return { stages: [], exitIds: incoming };
    const childPrefix = `${prefix}${node.id}-`;
    const v2 = new Set([...visited, node.templateId]);
    return tpl.pattern === "graph"
      ? expandGraph(tpl, ctx, childPrefix, incoming, v2)
      : expandSupervisor(tpl, ctx, childPrefix, incoming, v2);
  }
  // Solo node → one stage. If unresolvable, bridge deps through so the DAG stays connected.
  const { solo, provider } = resolveSolo(ctx, node.soloId);
  const st = stageFromSolo(ctx, prefix + node.id, solo, provider || undefined, incoming, {
    system: node.system, model: node.model, effort: node.effort, maxTokens: node.maxTokens, task: node.task,
  });
  return st ? { stages: [st], exitIds: [st.id] } : { stages: [], exitIds: incoming };
}

/**
 * Expand a Supervisor into a DAG: supervisor plan → workers (parallel) →
 * supervisor integrate. `incoming` attaches to the plan; the exit is integrate.
 */
function expandSupervisor(sup: SupervisorTemplate, ctx: CompileCtx, prefix: string, incoming: string[], _visited: Set<string>): Expansion {
  const s = resolveSolo(ctx, sup.supervisor);
  const plan = stageFromSolo(ctx, `${prefix}sup-plan`, s.solo, s.provider || undefined, incoming, {
    system: `${s.solo ? soloSystem(s.solo) : ""}\n\n${i18n.t("prompts.supervisor")}`,
  });
  if (!plan) return { stages: [], exitIds: incoming };
  const stages: RunStage[] = [plan];

  const workerIds: string[] = [];
  sup.workers.forEach((wid, i) => {
    const w = resolveSolo(ctx, wid);
    const st = stageFromSolo(ctx, `${prefix}worker-${i}-${wid}`, w.solo, w.provider || undefined, [plan.id], {
      system: `${w.solo ? soloSystem(w.solo) : ""}\n\n${i18n.t("prompts.worker")}`,
    });
    if (st) { stages.push(st); workerIds.push(st.id); }
  });

  const integrate = stageFromSolo(ctx, `${prefix}sup-integrate`, s.solo, s.provider || undefined, workerIds.length ? workerIds : [plan.id], {
    system: `${s.solo ? soloSystem(s.solo) : ""}\n\n${i18n.t("prompts.integrate")}`,
  });
  if (!integrate) return { stages, exitIds: [plan.id] };
  stages.push(integrate);
  return { stages, exitIds: [integrate.id] };
}

/** Compile a Graph template (nodes may be Solos or nested Templates) to a DAG. */
export function compileGraph(
  graph: GraphTemplate,
  solos: SoloAgent[],
  providers: ProviderInput[],
  tools: ToolDef[],
  templates: StaticTemplate[],
  task: string,
): RunStage[] {
  return expandGraph(graph, { solos, providers, tools, templates, task }, "", [], new Set()).stages;
}

/** Compile a Supervisor template (plan→workers→integrate) to a DAG. */
export function compileSupervisor(
  sup: SupervisorTemplate,
  solos: SoloAgent[],
  providers: ProviderInput[],
  tools: ToolDef[],
  templates: StaticTemplate[],
  task: string,
): RunStage[] {
  return expandSupervisor(sup, { solos, providers, tools, templates, task }, "", [], new Set()).stages;
}

/**
 * A single "router" stage for Dynamic orchestration: given the meta-orchestrator
 * prompt, the task and the available template catalog, it must emit a line
 * `SELECT: <templateId>`. The caller parses that from the run log and then runs
 * the chosen template.
 */
export function compileRouter(
  dynamicPrompt: string,
  provider: ProviderInput | undefined,
  model: string,
  catalog: { id: string; name: string; desc: string }[],
  task: string,
): RunStage[] {
  if (!provider) return [];
  const list = catalog.map((c) => `- ${c.id}: ${c.name} — ${c.desc}`).join("\n");
  return [{
    id: "router",
    name: "Router",
    role: "meta-orchestrator",
    model,
    provider: provider.dialect || "anthropic",
    providerPrefix: provider.prefix,
    system: `${dynamicPrompt}\n\n${i18n.t("prompts.router", { list })}`,
    task,
    dependsOn: [],
    tools: [],
  }];
}
