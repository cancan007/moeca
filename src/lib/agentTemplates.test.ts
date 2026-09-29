import { describe, it, expect } from "vitest";
import { templateOptions, normalizeRef, buildRunSpec, compileRef, sinkStageId, DYNAMIC_REF, type TemplateStores } from "./agentTemplates";
import { dialectSearches } from "@/features/delivery/review/compileTemplate";
import type { SoloAgent, StaticTemplate } from "./templates";
import type { RunStage } from "./sandbox";

const solo = (id: string, name: string): SoloAgent =>
  ({ id, name, role: "実装", providerId: "anthropic", model: "claude-sonnet-5", ctx: "", strat: "", dot: "", arch: "" }) as SoloAgent;

const graph = (id: string, name: string): StaticTemplate =>
  ({ id, name, desc: "", pattern: "graph", nodes: [{ id: "a" }, { id: "b" }], edges: [["a", "b"]] }) as StaticTemplate;

const stores = (): TemplateStores => ({
  solos: [solo("builder", "Builder"), solo("tester", "Tester")],
  staticTpls: [graph("impl", "実装フロー")],
  providers: [],
  tools: [],
});

describe("templateOptions", () => {
  it("lists single-agent and multi-agent templates in one namespace", () => {
    const refs = templateOptions(stores()).map((o) => o.ref);
    expect(refs).toEqual(["solo:builder", "solo:tester", "static:impl"]);
  });

  it("only offers Dynamic where a router can run", () => {
    expect(templateOptions(stores()).map((o) => o.ref)).not.toContain(DYNAMIC_REF);
    expect(templateOptions(stores(), { includeDynamic: true }).map((o) => o.ref)).toContain(DYNAMIC_REF);
  });
});

describe("normalizeRef", () => {
  it("passes through a ref that still resolves", () => {
    expect(normalizeRef("static:impl", stores())).toBe("static:impl");
    expect(normalizeRef("solo:tester", stores())).toBe("solo:tester");
  });

  // Assignments were stored as a bare template id before refs existed.
  it("upgrades a bare static-template id", () => {
    expect(normalizeRef("impl", stores())).toBe("static:impl");
  });

  it("upgrades the old Dynamic sentinel", () => {
    expect(normalizeRef("__dynamic__", stores())).toBe(DYNAMIC_REF);
  });

  // "" used to mean "the built-in bare agent", a shape that no longer exists.
  it("maps the old empty assignment onto a real template", () => {
    expect(normalizeRef("", stores())).toBe("solo:builder");
  });

  it("falls back when a template was deleted", () => {
    expect(normalizeRef("static:deleted", stores())).toBe("solo:builder");
  });

  it("yields an empty ref when nothing is configured", () => {
    const empty: TemplateStores = { solos: [], staticTpls: [], providers: [], tools: [] };
    expect(normalizeRef("", empty)).toBe("");
    expect(normalizeRef("solo:gone", empty)).toBe("");
  });
});

describe("Dynamic needs something to route to", () => {
  it("is not a valid fallback when no templates exist", () => {
    const empty: TemplateStores = { solos: [], staticTpls: [], providers: [], tools: [] };
    expect(normalizeRef(DYNAMIC_REF, empty)).toBe("");
  });

  it("stays selected when templates do exist", () => {
    expect(normalizeRef(DYNAMIC_REF, stores())).toBe(DYNAMIC_REF);
  });
});

// The orchestrator refuses a shared worktree with concurrency, because the
// stages would write over each other. This spec is what every scheduled run is
// submitted as, so a combination it rejects means no Daily schedule can start
// at all — which is exactly what happened: a 400 before any container existed.
describe("buildRunSpec is a spec the orchestrator accepts", () => {
  it("does not ask for concurrency on a shared worktree", () => {
    const spec = buildRunSpec([]);
    expect(spec.worktreeMode).toBe("shared");
    expect(spec.maxParallel).toBe(1);
  });

  it("marks a scheduled run unattended, and a Delivery run not", () => {
    expect(buildRunSpec([], { unattended: true })).toMatchObject({ unattended: true });
    expect(buildRunSpec([])).not.toHaveProperty("unattended");
  });
});

// A chat reply is one stage's closing message, so the stage has to be named.
describe("sinkStageId", () => {
  const stage = (id: string, dependsOn: string[] = []) =>
    ({ id, name: id, role: "", model: "", provider: "", providerPrefix: "", system: "", task: "", dependsOn }) as RunStage;

  it("is the stage nothing depends on", () => {
    expect(sinkStageId([stage("plan"), stage("build", ["plan"]), stage("review", ["build"])])).toBe("review");
  });

  it("is the only stage of a one-stage template", () => {
    expect(sinkStageId([stage("builder")])).toBe("builder");
  });

  // Several sinks means several answers written without knowing about each
  // other; the last one stands rather than concatenating them.
  it("takes the last sink when a run fans out at the end", () => {
    expect(sinkStageId([stage("plan"), stage("a", ["plan"]), stage("b", ["plan"])])).toBe("b");
  });

  it("has nothing to name for an empty run", () => {
    expect(sinkStageId([])).toBe("");
  });
});

// Which dialects can actually perform a provider-side web search. The compiler
// and the Settings switch ask the same question of the same function, so a grant
// cannot be offered where the stage would drop it.
describe("dialectSearches", () => {
  it("is true where the dialect has the tool", () => {
    expect(dialectSearches("anthropic")).toBe(true);
    expect(dialectSearches("openai-responses")).toBe(true);
    // An unset dialect means Anthropic, which is what the stage compiles to.
    expect(dialectSearches(undefined)).toBe(true);
    expect(dialectSearches("")).toBe(true);
  });

  it("is false where the request would carry a tool nothing executes", () => {
    expect(dialectSearches("openai")).toBe(false);
    expect(dialectSearches("gemini")).toBe(false);
  });
});

describe("compiling the web search grant", () => {
  const searcher = (providerId: string): SoloAgent =>
    ({ ...solo("s", "Searcher"), providerId, web: { maxUses: 3 } }) as SoloAgent;
  const withProviders = (dialect: string): TemplateStores => ({
    ...stores(),
    solos: [searcher("p")],
    providers: [{ name: "p", kind: "model", dialect, prefix: `/${dialect}/`, upstream: "", allowlist: [], models: [], injectHeaders: {} }],
  });

  it("carries the grant to a dialect that searches", () => {
    for (const dialect of ["anthropic", "openai-responses"]) {
      const compiled = compileRef("solo:s", withProviders(dialect), "find it");
      expect(compiled?.stages[0].web).toEqual({ maxUses: 3 });
    }
  });

  // Compiled honestly rather than hopefully: an agent that cannot search answers
  // from what it knows instead of appearing to have searched.
  it("drops it for a dialect that would ignore it", () => {
    for (const dialect of ["openai", "gemini"]) {
      const compiled = compileRef("solo:s", withProviders(dialect), "find it");
      expect(compiled?.stages[0].web).toBeUndefined();
    }
  });
});
