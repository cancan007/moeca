// Client for the host agent's Chat API: standing conversations whose turns run
// as ordinary orchestrator runs.
//
// The transcript lives on the host, not here. A conversation is durable state
// that has to survive a reload, be searchable, and be readable by the compactor
// (which runs host-side because that is where the gateway session is) — none of
// which localStorage does, and a long conversation would eventually be dropped
// by a storage quota with no warning.
//
// What the frontend still owns is compilation: the run spec for a turn is built
// here from the template stores, exactly as Delivery and Daily build theirs. The
// host never learns what a stage is.

import type { Artifact } from "@/lib/daily";
import type { KnowledgeScope } from "@/lib/schedules";

const BASE = import.meta.env.VITE_HOSTAGENT_URL ?? "http://127.0.0.1:8788";

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

export interface Conversation {
  id: string; // "chat-<n>"
  title: string;
  /** The bound agent template as a compilable ref ("solo:<id>"). Stored as a ref
   *  rather than an agent id so binding a multi-agent template needs no change
   *  on either side. */
  templateRef: string;
  templateLabel: string;
  scope?: KnowledgeScope;
  createdAt: string;
  updatedAt: string;
  /** Rail summaries: how long the conversation is (compaction does not shorten
   *  this) and the last thing said in it. */
  turnCount: number;
  lastText: string;
}

/** One entry of a conversation. A `summary` turn stands in for the range
 *  [coversFrom, coversTo], whose own turns are still here, marked `dropped`. */
export interface Turn {
  id: number;
  conversationId: string;
  seq: number;
  kind: "user" | "agent" | "summary";
  text: string;
  /** What this message was replying to (user turns only): a passage selected in
   *  an answer, or a region marked on an image. Kept beside the message rather
   *  than pasted into it, so the transcript shows the reply as it was written. */
  quotes?: string[];
  runId?: string;
  outputDir?: string;
  /** Agent turns only: running | done | failed | stopped | empty. */
  status?: string;
  agent?: string;
  tokensEst: number;
  dropped: boolean;
  coversFrom?: number;
  coversTo?: number;
  createdAt: string;
}

export interface TurnPair {
  userTurn: Turn;
  agentTurn: Turn;
  runId: string;
}

export interface CompactResult {
  compacted: boolean;
  reason?: string;
  mode?: string;
  /** What the compaction actually bought back, reported by the host rather than
   *  predicted here — the number shown to the operator should be the one that
   *  happened. */
  replacedTokens?: number;
  summaryTokens?: number;
  coversFrom?: number;
  coversTo?: number;
  turns?: Turn[];
}

export const chat = {
  conversations: () =>
    req<{ conversations: Conversation[] }>("/chat/conversations").then((r) => r.conversations),

  /** Create a conversation, or update one when `id` is given. Title, bound agent
   *  and scope are all mutable; a change applies from the next turn rather than
   *  rewriting what earlier turns ran as. */
  save: (c: Partial<Conversation> & { id?: string }) =>
    req<Conversation>("/chat/conversation", { method: "POST", body: JSON.stringify(c) }),

  remove: (id: string) =>
    req<{ deleted: string }>(`/chat/conversation?id=${encodeURIComponent(id)}`, { method: "DELETE" }),

  turns: (conversation: string) =>
    req<{ turns: Turn[] }>(`/chat/turns?conversation=${encodeURIComponent(conversation)}`).then((r) => r.turns),

  /**
   * Record a message and launch the run that answers it.
   *
   * `runSpec` is already compiled, and `sinkStage` names the stage whose closing
   * message is the reply — the host reads it out of that stage's handoff
   * manifest. Without it the host falls back to the last manifest written, which
   * is right in a serial run but guesses where it could know.
   */
  send: (body: {
    conversation: string;
    text: string;
    quotes: string[];
    agent: string;
    sinkStage: string;
    runSpec: unknown;
  }) => req<TurnPair>("/chat/turn", { method: "POST", body: JSON.stringify(body) }),

  /** One turn as the host now holds it — polled to learn that a reply landed.
   *  Live stage-by-stage progress comes from the sandbox controller instead,
   *  which is the component that actually knows it. */
  turn: (id: number) => req<Turn>(`/chat/turn?id=${id}`),

  /** Reduce the conversation's live history. "recent" drops old turns for free;
   *  "sum" replaces them with a briefing from the compactor agent. A failure
   *  leaves the transcript untouched and says so. */
  compact: (body: {
    conversation: string;
    mode: "recent" | "sum";
    keepTurns: number;
    prefix?: string;
    model?: string;
    system?: string;
  }) => req<CompactResult>("/chat/compact", { method: "POST", body: JSON.stringify(body) }),

  /** What the conversation's working directory holds. Every turn shares it, so
   *  this is the conversation's output rather than one message's. */
  artifacts: (conversation: string) =>
    req<{ artifacts: Artifact[] }>(`/chat/artifacts?conversation=${encodeURIComponent(conversation)}`)
      .then((r) => r.artifacts),

  /** URL of one artifact's bytes — used directly as an <img>/<video> src and as
   *  the download link. Only media is served inline; anything else comes back as
   *  a download whatever this flag says. */
  artifactUrl: (conversation: string, path: string, download = false) =>
    `${BASE}/chat/artifact?conversation=${encodeURIComponent(conversation)}&path=${encodeURIComponent(path)}${
      download ? "&download=1" : ""
    }`,

  /** Hand the conversation a file. It lands in the working directory, where the
   *  next turn's agent can read it — from inside the container a file someone
   *  attached and a file it wrote are both just contents of /work. */
  attach: async (conversation: string, file: File) => {
    const form = new FormData();
    form.append("file", file);
    const res = await fetch(`${BASE}/chat/attachment?conversation=${encodeURIComponent(conversation)}`, {
      method: "POST",
      body: form, // no Content-Type: the browser sets the multipart boundary
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
    }
    return res.json() as Promise<{ name: string; size: number }>;
  },
};
