// Client for the RAG indexer's host-facing management API (loopback). The
// indexer holds the knowledge (read-only mount) and answers agent searches only
// through the gateway; this client is for the UI to reindex / inspect / test.

const BASE = import.meta.env.VITE_RAG_URL ?? "http://127.0.0.1:8790";

export type RagKind = "local" | "external";
/** Retrieval scope. "global" is the default and the only one that bypasses the
 *  group filter — everything else is subject to the run's granted groups.
 *
 *  It is reported, not set: the indexer derives it from group membership, so a
 *  source nobody has assigned reads "global" and one that has been assigned on
 *  the Knowledge screen reads "project". There is no control for it, by design
 *  — assigning the source IS the gesture that narrows it. */
export type RagScope = "global" | "project" | "organization";

/** What the source file is. */
export type RagMedia = "text" | "csv" | "subtitle" | "pdf" | "image" | "video";
/** What was actually indexed for it — a different question from what it is.
 *
 *  "metadata" means only the path and filename were embedded: the file's own
 *  contents are NOT searchable. Rendering that the same as "text" would tell
 *  the user a screenshot's contents are in the index when they are not.
 *
 *  "caption" means a vision model looked at the picture and its description was
 *  indexed. Searchable by content, but the words are a model's account of the
 *  image rather than anything written in it — which is why it is not "text"
 *  either. Only images, and only when an operator has turned captioning on. */
export type RagContent = "text" | "metadata" | "caption";

export interface RagSource {
  path: string;
  chunks: number;
  /** local file mount vs external HTTPS document. Older indexers omit it → local. */
  kind?: RagKind;
  /** which scope the knowledge belongs to. Older indexers omit it → project. */
  scope?: RagScope;
  /** set for external sources. */
  url?: string;
  /** per-source ingestion error (e.g. failed fetch), if any. */
  error?: string;
  /** file class. Older indexers omit it → treat as text. */
  media?: RagMedia;
  /** what was indexed. Older indexers omit it → treat as text. */
  content?: RagContent;
  /** non-fatal remark: truncation, a PDF with no text layer, a video with no
   *  caption track. Unlike `error` the source is still usable. */
  note?: string;
}

/** Badge metadata per media class. `labelKey` is an i18n key, not a label —
 *  this module stays render-agnostic and the caller translates. */
export const RAG_MEDIA: Record<RagMedia, { labelKey: string; color: string }> = {
  text: { labelKey: "rag.media.text", color: "#67c9a4" },
  csv: { labelKey: "rag.media.csv", color: "#34d3e0" },
  subtitle: { labelKey: "rag.media.subtitle", color: "#34d3e0" },
  pdf: { labelKey: "rag.media.pdf", color: "#e0a83e" },
  image: { labelKey: "rag.media.image", color: "#b08ad9" },
  video: { labelKey: "rag.media.video", color: "#b08ad9" },
};

/** Scope display metadata, in render order. `hint` is a fixed English tag
 *  (default / secure / broad / manual) and stays as-is in every language. */
export const RAG_SCOPES: { id: RagScope; labelKey: string; hint: string; color: string }[] = [
  { id: "global", labelKey: "rag.scopes.global", hint: "default", color: "#67c9a4" },
  { id: "project", labelKey: "rag.scopes.project", hint: "secure", color: "#34d3e0" },
  { id: "organization", labelKey: "rag.scopes.organization", hint: "broad", color: "#e0a83e" },
];

export interface RagStatus {
  chunks: number;
  sources: RagSource[];
  building: boolean;
  lastError: string;
  builtAt: string;
  /** where the vectors came from. "offline" means they were computed locally
   *  without a model — usable for a demo, not for real retrieval. Older
   *  indexers omit it → gateway. */
  embedMode?: "gateway" | "offline";
  /** From the last build: chunks re-embedded, and chunks that kept the vector
   *  they already had. A rebuild that paid for everything and one that paid for
   *  nothing are otherwise indistinguishable. Older indexers omit both. */
  embedded?: number;
  reused?: number;
}

export interface RagResult {
  source: string;
  text: string;
  score: number;
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, { ...init, headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) } });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

export const rag = {
  async health(): Promise<boolean> {
    try {
      return (await fetch(BASE + "/health")).ok;
    } catch {
      return false;
    }
  },
  // The indexer marshals empty lists as JSON null before its first build, so
  // normalize here — the panel renders these straight into .length / .map.
  async status(): Promise<RagStatus> {
    const s = await req<RagStatus>("/status");
    return { ...s, sources: s.sources ?? [] };
  },
  reindex: () => req<{ status: string }>("/index", { method: "POST" }),
  // Direct test search (UI only). Agents search via the gateway's /rag route.
  async search(query: string, k = 5): Promise<{ results: RagResult[] }> {
    const r = await req<{ results: RagResult[] }>("/search", { method: "POST", body: JSON.stringify({ query, k }) });
    return { results: r.results ?? [] };
  },
};
