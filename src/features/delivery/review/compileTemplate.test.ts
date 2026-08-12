import { describe, it, expect } from "vitest";
import { compileSolo, compileGraph, compileSupervisor } from "./compileTemplate";
import type { SoloAgent, GraphTemplate, SupervisorTemplate } from "@/lib/templates";
import type { ToolDef } from "@/lib/tools";
import type { ProviderInput } from "@/lib/providers";

// Compiling a template down to the Stage DAG is the seam where an authored
// template becomes something the sandbox controller will run. These tests pin
// the part that decides WHAT each stage is: an LLM agent, or a command.

const agent = (id: string, name = id): SoloAgent =>
  ({ id, name, role: "実装", providerId: "anthropic", model: "claude-sonnet-5", ctx: "128k", strat: "", dot: "", arch: "generic" }) as SoloAgent;

const command = (id: string, cmd: string, image?: string): SoloAgent =>
  ({ ...agent(id), kind: "command", cmd, image }) as SoloAgent;

const providers: ProviderInput[] = [
  { name: "anthropic", kind: "model", dialect: "anthropic", prefix: "/anthropic/", upstream: "https://api.anthropic.com", allowlist: [], models: ["claude-sonnet-5"], injectHeaders: {} },
];

describe("command atoms", () => {
  it("compiles to a stage that names an image policy and a command", () => {
    const [stage] = compileSolo(command("build", "npm ci && npm test", "poly"), providers, [], "リリース準備");

    expect(stage.image).toBe("poly");
    expect(stage.cmd).toEqual(["bash", "-lc", "npm ci && npm test"]);
    // The task text reaches the command as $ORCHESTRA_TASK, so a schedule's
    // goal is still available to a stage with no model in it.
    expect(stage.task).toBe("リリース準備");
  });

  // The stage carries no model configuration at all: the orchestrator only sets
  // ORCHESTRA_MODEL / _PROVIDER / _BASE_URL when they are non-empty, so a
  // command container ends up with none of them.
  it("carries no model, provider or tools", () => {
    const [stage] = compileSolo(command("build", "make"), providers, [], "t");
    expect(stage.model).toBe("");
    expect(stage.provider).toBe("");
    expect(stage.providerPrefix).toBe("");
    expect(stage.system).toBe("");
    expect(stage.tools).toEqual([]);
  });

  // A command atom has no provider, and the agent path drops a solo whose
  // provider does not resolve. If that check applied here, a build or transcode
  // step would vanish from the middle of a DAG without a word.
  it("compiles even with no providers configured", () => {
    const stages = compileSolo(command("build", "make"), [], [], "t");
    expect(stages).toHaveLength(1);
    expect(stages[0].cmd).toEqual(["bash", "-lc", "make"]);
  });

  it("is dropped when it has no command to run", () => {
    expect(compileSolo(command("build", "   "), providers, [], "t")).toEqual([]);
  });

  it("leaves the image unset when none was chosen, so the controller decides", () => {
    const [stage] = compileSolo(command("build", "make"), providers, [], "t");
    expect(stage.image).toBeUndefined();
  });
});

// Graph nodes and Supervisor workers both bind to a Solo, which is why putting
// `kind` on the Solo makes command stages expressible in every template shape
// rather than only in the one that was wired first.
describe("command atoms reach every template shape", () => {
  it("mixes agent and command stages in a graph", () => {
    const g: GraphTemplate = {
      id: "release", name: "リリース", desc: "", pattern: "graph",
      nodes: [{ id: "plan", soloId: "planner" }, { id: "build", soloId: "builder" }, { id: "review", soloId: "reviewer" }],
      edges: [["plan", "build"], ["build", "review"]],
    };
    const solos = [agent("planner"), command("builder", "npm run build", "poly"), agent("reviewer")];
    const stages = compileGraph(g, solos, providers, [], [], "タスク");

    expect(stages.map((s) => s.id)).toEqual(["plan", "build", "review"]);
    expect(stages[0].cmd).toBeUndefined();
    expect(stages[1].cmd).toEqual(["bash", "-lc", "npm run build"]);
    expect(stages[1].image).toBe("poly");
    expect(stages[2].cmd).toBeUndefined();
    // The DAG wiring is untouched by the kind of each node.
    expect(stages[1].dependsOn).toEqual(["plan"]);
    expect(stages[2].dependsOn).toEqual(["build"]);
  });

  it("lets a supervisor delegate to a command worker", () => {
    const sup: SupervisorTemplate = {
      id: "media", name: "メディア", desc: "", pattern: "supervisor",
      supervisor: "lead", workers: ["encoder"],
    };
    const solos = [agent("lead"), command("encoder", "ffmpeg -i in.mov out.mp4", "media")];
    const stages = compileSupervisor(sup, solos, providers, [], [], "動画を書き出す");

    const worker = stages.find((s) => s.id.includes("worker"));
    expect(worker?.cmd).toEqual(["bash", "-lc", "ffmpeg -i in.mov out.mp4"]);
    expect(worker?.image).toBe("media");
    // plan → worker → integrate still holds.
    expect(stages.map((s) => s.id)).toEqual(["sup-plan", "worker-0-encoder", "sup-integrate"]);
  });
});

describe("agent atoms are unchanged", () => {
  it("still resolves provider, model and system prompt", () => {
    const [stage] = compileSolo(agent("builder", "Builder"), providers, [], "タスク");
    expect(stage.model).toBe("claude-sonnet-5");
    expect(stage.provider).toBe("anthropic");
    expect(stage.providerPrefix).toBe("/anthropic/");
    expect(stage.image).toBeUndefined();
    expect(stage.cmd).toBeUndefined();
  });

  it("is still dropped when its provider does not resolve", () => {
    expect(compileSolo(agent("builder"), [], [], "t")).toEqual([]);
  });
});

describe("web search grant", () => {
  const searcher = { ...agent("researcher"), web: { maxUses: 3 } } as SoloAgent;

  it("reaches the stage when the agent runs on Anthropic", () => {
    const [stage] = compileSolo(searcher, providers, [], "t");
    expect(stage.web).toEqual({ maxUses: 3 });
  });

  // web_search is an Anthropic server tool. Compiling the grant onto an OpenAI
  // or Gemini stage would put a tool in the run spec that neither the provider
  // nor the agent executes — the agent would answer from memory while the run
  // spec claimed it could search.
  it("is dropped for providers that have no server-side search", () => {
    const openai: ProviderInput[] = [
      { name: "anthropic", kind: "model", dialect: "openai", prefix: "/openai/", upstream: "https://api.openai.com", allowlist: [], models: ["claude-sonnet-5"], injectHeaders: {} },
    ];
    const [stage] = compileSolo(searcher, openai, [], "t");
    expect(stage.web).toBeUndefined();
  });

  it("is absent for an agent that was never granted it", () => {
    const [stage] = compileSolo(agent("builder"), providers, [], "t");
    expect(stage.web).toBeUndefined();
  });
});

// Generation is a tool now, so it routes wherever its own definition says —
// independently of the provider the agent reasons with. It used to inherit the
// agent's prefix, so a Claude agent asked for gpt-image-1 at
// /anthropic/v1/images/generations, an endpoint that does not exist.
describe("artifact tools route independently of the agent's own provider", () => {
  const multi: ProviderInput[] = [
    ...providers,
    { name: "openai", kind: "model", dialect: "openai", prefix: "/openai/", upstream: "https://api.openai.com", allowlist: [], models: ["gpt-4o"], injectHeaders: {} },
  ];
  const imageTool: ToolDef = {
    id: "gen-image", name: "generate_image", description: "draw",
    params: [{ name: "prompt", type: "string", description: "", required: true }],
    method: "POST", path: "/openai/v1/images/generations", headers: {},
    body: '{"model":"gpt-image-1","prompt":"{{prompt}}"}', targetHeader: "",
    output: { kind: "base64", jsonPath: "data.0.b64_json", extensions: [".png"] },
  };

  it("keeps the tool's own route while the agent thinks with another provider", () => {
    const solo = { ...agent("designer"), toolIds: ["gen-image"] } as SoloAgent;
    const [stage] = compileSolo(solo, multi, [imageTool], "犬の画像を作る");

    expect(stage.providerPrefix).toBe("/anthropic/"); // reasoning stays on Claude
    const tool = stage.tools?.find((t) => t.name === "generate_image");
    expect(tool?.path).toBe("/openai/v1/images/generations"); // generation does not
    expect(tool?.output?.kind).toBe("base64");
    expect(tool?.output?.jsonPath).toBe("data.0.b64_json");
  });

  // A text tool's compiled shape is unchanged, so an agent that predates
  // artifact outputs sees exactly what it saw.
  it("carries no output field for an ordinary tool", () => {
    const plain: ToolDef = { ...imageTool, id: "ping", name: "ping", output: undefined };
    const solo = { ...agent("caller"), toolIds: ["ping"] } as SoloAgent;
    const [stage] = compileSolo(solo, multi, [plain], "t");
    expect(stage.tools?.[0]?.output).toBeUndefined();
  });
});

// The supervisor plans blind unless it is told what it commands. Asked for a
// five-second cat video, one divided the job across three workers when the
// template had exactly one — so the stage that was to encode the video never
// existed, and nothing produced it. It also reasoned its way to drawing frames
// with OpenCV, not knowing that its single worker held a generate_video tool.
describe("the supervisor is told what it commands", () => {
  const videoTool: ToolDef = {
    id: "gen-video", name: "generate_video", description: "", params: [],
    method: "POST", path: "/openai/v1/videos", headers: {}, body: "{}", targetHeader: "",
    output: { kind: "binary", extensions: [".mp4"] },
  };
  const sup = (workers: string[]): SupervisorTemplate =>
    ({ id: "t", name: "team", desc: "", pattern: "supervisor", supervisor: "lead", workers }) as SupervisorTemplate;

  const planOf = (stages: { id: string; system: string }[]) => stages.find((s) => s.id === "sup-plan")!.system;

  it("names every worker, and only the workers that exist", () => {
    const solos = [agent("lead"), { ...agent("drawer"), toolIds: ["gen-video"] } as SoloAgent];
    const stages = compileSupervisor(sup(["drawer"]), solos, providers, [videoTool], [], "猫の動画");
    const system = planOf(stages);

    expect(system).toContain("worker-0-drawer");
    // The count has to be the real one: "3 workers" is what produced a plan
    // with a stage that never ran.
    expect(system).toContain("1");
  });

  // The capability that would have done the job in one call.
  it("lists the tools each worker holds", () => {
    const solos = [agent("lead"), { ...agent("drawer"), toolIds: ["gen-video"] } as SoloAgent];
    const system = planOf(compileSupervisor(sup(["drawer"]), solos, providers, [videoTool], [], "t"));
    expect(system).toContain("generate_video");
  });

  // Scripts were written that nothing could run: an agent stage has no shell.
  it("says when nothing in the composition can execute code", () => {
    const solos = [agent("lead"), agent("drawer")];
    const system = planOf(compileSupervisor(sup(["drawer"]), solos, providers, [], [], "t"));
    expect(system.toLowerCase()).toMatch(/実行|execute/);
  });

  it("describes a command worker by what it runs", () => {
    const solos = [agent("lead"), command("encoder", "ffmpeg -i in.png out.mp4", "media")];
    const system = planOf(compileSupervisor(sup(["encoder"]), solos, providers, [], [], "t"));
    expect(system).toContain("ffmpeg -i in.png out.mp4");
    expect(system).toContain("media");
  });

  // Wiring is unchanged: plan first, then workers, then integrate.
  it("keeps the DAG shape", () => {
    const solos = [agent("lead"), agent("a"), agent("b")];
    const stages = compileSupervisor(sup(["a", "b"]), solos, providers, [], [], "t");
    expect(stages.map((s) => s.id)).toEqual(["sup-plan", "worker-0-a", "worker-1-b", "sup-integrate"]);
    expect(stages[1].dependsOn).toEqual(["sup-plan"]);
    expect(stages[3].dependsOn).toEqual(["worker-0-a", "worker-1-b"]);
  });
});
