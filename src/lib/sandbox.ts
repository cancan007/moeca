// Client for the Orchestra sandbox controller (Docker-isolated agent runs).
// Runs on loopback as a Tauri sidecar; in dev: `cd sandbox && go run . -config config.json`.

import type { CompiledTool } from "@/lib/tools";

const BASE = import.meta.env.VITE_SANDBOX_URL ?? "http://127.0.0.1:8789";

/** One node of a run DAG, as sent to the orchestrator. */
export interface RunStage {
  id: string;
  name: string;
  role: string;
  model: string;
  /** Reasoning effort (cost lever): low|medium|high|xhigh|max. Omit => the
   * agent's default (medium). */
  effort?: string;
  /** Per-response output-token cap. Omit => the agent default (16000). */
  maxTokens?: number;
  /** LLM dialect (anthropic|openai|gemini) and its gateway route prefix. */
  provider: string;
  providerPrefix: string;
  system: string;
  task: string;
  dependsOn: string[];
  /** Custom HTTP tools (through the gateway) exposed to this stage's agent. */
  tools?: CompiledTool[];
  /** Media generation this stage may do. Each configured kind becomes one tool
   *  (generate_image / generate_speech / generate_video); an omitted kind is an
   *  absent tool, not a disabled one. Routed through the gateway like every
   *  other model call, so the agent still holds no key. */
  media?: MediaTools;
  /** Web search grant. Unlike every other tool, the agent does not perform this
   *  one: the model provider runs the search and returns the results inside the
   *  same response, so the container gets no route to the web and the gateway
   *  sees the model call it already sees. Omit => the agent has no search tool
   *  at all. Anthropic-dialect stages only — the other providers have no
   *  equivalent, and the agent drops the grant rather than advertise a tool
   *  nothing executes. */
  web?: WebSearch;
  /**
   * Name of an entry in the controller's image allowlist ("base" | "poly" |
   * "media" | a custom one added in Settings). It is a policy name, never an
   * image reference — the controller decides the ref, the network posture, the
   * resource caps and the scratch mounts. Omit => the default policy.
   */
  image?: string;
  /**
   * Replaces the image's default command, so a stage can run a build, a test
   * suite or a transcode instead of an LLM agent loop (the "poly" and "media"
   * images ship a shell for this). The command runs under the same hardening as
   * every other sandbox.
   */
  cmd?: string[];
}

export interface RunSpec {
  taskId: string;
  worktreePath: string;
  isolation?: "strict" | "relaxed";
  /**
   * "shared" (default) mounts one worktree into every stage; stages hand off
   * through it. "isolated" gives each stage its own git worktree seeded from its
   * dependencies' output, so parallel stages never clobber each other, and the
   * run's sink stages are merged back into the base worktree at the end.
   */
  worktreeMode?: "shared" | "isolated";
  /**
   * Enable runtime supervisor delegation: each stage's agent gets a
   * spawn_subagent tool (file-based, no network to the host) and the controller
   * runs the requested sub-agents in the stage's worktree.
   */
  delegation?: boolean;
  /**
   * Marks a run nobody is watching — a Daily schedule firing, as opposed to a
   * reviewer starting one from the Delivery drawer. It restricts the run to
   * images explicitly approved for unattended use, so a schedule can never
   * silently start executing an image someone added while debugging. It only
   * ever narrows what is permitted.
   */
  unattended?: boolean;
  maxParallel?: number;
  stopOnFailure?: boolean;
  stages: RunStage[];
}

/** One generation route: a gateway prefix plus the model to ask for. Defaults
 *  (voice, size, duration) are per-template so a run does not depend on the
 *  model guessing them. */
export interface MediaSpec {
  prefix: string;
  model: string;
  path?: string;
  voice?: string;
  size?: string;
  format?: string;
  seconds?: string;
}

export interface MediaTools {
  image?: MediaSpec;
  speech?: MediaSpec;
  video?: MediaSpec;
}

/** The web_search grant. There is no route here because there is nothing to
 *  route: the search runs on the model provider's side. What it does carry is a
 *  budget — searches are billed per use, not per token — and an optional domain
 *  restriction. allowedDomains and blockedDomains are mutually exclusive on the
 *  wire; when both are set the agent honours the narrower one (allowed). */
export interface WebSearch {
  /** Searches allowed in one run. Omit => the agent's default (5). */
  maxUses?: number;
  allowedDomains?: string[];
  blockedDomains?: string[];
}

export type StageStatus = "pending" | "running" | "done" | "failed" | "skipped" | "stopped";

/** One path a stage touched, with its line counts (-1 for binary files). */
export interface StageFileChange {
  path: string;
  additions: number;
  deletions: number;
}

export interface StageState {
  id: string;
  name: string;
  role: string;
  dependsOn: string[];
  containerId: string;
  status: StageStatus;
  exitCode: number;
  /** The commit recording this stage's output, the commit it built on, and the
   *  files it touched. Absent when the stage changed nothing. */
  commit?: string;
  parent?: string;
  files?: StageFileChange[];
  /** The image policy this stage ran under, and the immutable digest its
   *  reference resolved to at launch. Tags move; the digest is what makes
   *  "which bytes actually ran" answerable afterwards. */
  image?: string;
  imageDigest?: string;
}

export interface RunStatus {
  id: string;
  taskId: string;
  status: "running" | "done" | "failed" | "stopped";
  maxParallel: number;
  stages: StageState[];
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export const sandbox = {
  async health(): Promise<boolean> {
    try {
      return (await fetch(BASE + "/health")).ok;
    } catch {
      return false;
    }
  },
  // Every run is an orchestrated template run (a generic Stage DAG) — a single
  // agent is a one-stage template, so the single-container /sandbox routes have
  // no client here.
  runTemplate: (spec: RunSpec) =>
    req<{ runId: string }>("/run", { method: "POST", body: JSON.stringify(spec) }),
  runStatus: (id: string) =>
    req<RunStatus>(`/run?id=${encodeURIComponent(id)}`),
  runLogs: (id: string) =>
    req<{ id: string; logs: Record<string, string> }>(`/run/logs?id=${encodeURIComponent(id)}`),
  runStop: (id: string) =>
    req<{ stopping: string }>("/run/stop", { method: "POST", body: JSON.stringify({ id }) }),
  runRemove: (id: string) =>
    req<{ removed: string }>(`/run?id=${encodeURIComponent(id)}`, { method: "DELETE" }),

  /** How long finished runs' logs + metadata are kept. 0 = keep everything. */
  retention: () => req<Retention>("/retention"),
  setRetention: (days: number) =>
    req<Retention>("/retention", { method: "POST", body: JSON.stringify({ days }) }),

  /** The container-image allowlist a stage may choose from. */
  images: () => req<ImageList>("/images"),
  /** Add or update a custom image (built-in names are refused). Setting
   *  `unattended` is the explicit promotion that lets Daily schedules use it. */
  saveImage: (policy: ImagePolicyInput) =>
    req<{ images: ImagePolicy[] }>("/images", { method: "POST", body: JSON.stringify(policy) }),
  deleteImage: (name: string) =>
    req<{ images: ImagePolicy[] }>(`/images?name=${encodeURIComponent(name)}`, { method: "DELETE" }),
};

export interface Retention {
  days: number;
  defaultDays: number;
}

/**
 * One entry of the controller's image allowlist.
 *
 * A stage names a policy; the controller supplies everything else. That is what
 * keeps bringing your own image a supply-chain question rather than an isolation
 * one — nothing here reaches the container hardening, which is identical for
 * every image.
 */
export interface ImagePolicy {
  name: string;
  ref: string;
  description?: string;
  /** "egress" follows the run's isolation; "none" attaches no network at all
   *  (used by the media image, which parses untrusted input). */
  network?: "egress" | "none";
  memoryMB?: number;
  cpus?: number;
  pidsLimit?: number;
  tmpfs?: string[];
  /** Approved for scheduled (Daily) runs, where nobody is watching. */
  unattended?: boolean;
  /** Added at runtime from Settings, as opposed to shipped with the install. */
  custom?: boolean;
}

export type ImagePolicyInput = Omit<ImagePolicy, "custom">;

export interface ImageList {
  images: ImagePolicy[];
  default: string;
  maxMemoryMB: number;
  maxCPUs: number;
  maxPidsLimit: number;
}
