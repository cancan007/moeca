// Clients for the Knowledge screen.
//
// The screen reads from two services, and which one owns what is the whole
// arrangement in miniature:
//
//   hostagent  — the graph the user authors (organizations, projects, groups,
//                relations). Persisted, editable while the indexer is down.
//   ragindex   — what the index actually contains: where each source sits in
//                embedding space and which sources are near it.
//
// Nothing here goes through the gateway. These are host-facing management
// routes; the gateway forwards only /search and /source to the indexer,
// precisely because these enumerate every source regardless of group. The two
// it does forward are both filtered by the caller's granted groups — one finds
// a source, the other follows it — while these answer about the whole index.

const HOST = import.meta.env.VITE_HOSTAGENT_URL ?? "http://127.0.0.1:8788";
const RAG = import.meta.env.VITE_RAG_URL ?? "http://127.0.0.1:8790";

async function call<T>(base: string, path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(base + path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
  }
  return res.json() as Promise<T>;
}

// --- the authored graph ---

export interface KnowledgeOrg {
  id: string;
  name: string;
}

export interface KnowledgeProject {
  id: string;
  name: string;
  orgId: string;
}

export interface KnowledgeGroup {
  id: string;
  name: string;
  color: string;
  owner: string;
  description: string;
  /** projects this group serves; a group may serve several. */
  projects: string[];
  /** indexed sources this group contains — its retrieval permission set. */
  sources: string[];
}

/** The link kinds the graph can draw, strongest claim first.
 *
 *  Ordering is for reading, not for permission: none of these widen a scope.
 *  The host agent decides which types exist; this list only has to agree. */
export const RELATION_TYPES = [
  "requires",
  "derives-from",
  "same-as",
  "references",
  "supersedes",
  "conflicts-with",
] as const;

export type RelationType = (typeof RELATION_TYPES)[number];

/** Colour and dash per relation type, so the canvas and the inspector agree.
 *
 *  Solid for the edges that assert a positive relationship, dashed for the ones
 *  that qualify or warn. None of them carry permission — a scope is the whole of
 *  what a run may reach — so the styling says what the edge means and nothing
 *  about what it grants. */
export const RELATION_STYLE: Record<RelationType, { color: string; dash: string }> = {
  requires: { color: "var(--ac)", dash: "0" },
  "derives-from": { color: "var(--purple)", dash: "0" },
  "same-as": { color: "var(--cyan)", dash: "0" },
  references: { color: "var(--tx-dim)", dash: "2 3" },
  supersedes: { color: "var(--amber)", dash: "5 3" },
  "conflicts-with": { color: "var(--red)", dash: "5 3" },
};

/** What each relation type asserts. Vocabulary, not permission.
 *
 *  Relations grant nothing: a task's scope is the whole of what its agents may
 *  reach. This exists so the canvas can say what an edge means, which is the
 *  only thing it does. */
export const RELATION_MEANING: Record<RelationType, string> = {
  requires: "needs",
  "derives-from": "provenance",
  "same-as": "identity",
  references: "mention",
  supersedes: "replacement",
  "conflicts-with": "disagreement",
};
export interface KnowledgeRelation {
  id: string;
  from: string;
  to: string;
  type: RelationType;
}

export interface KnowledgeGraph {
  orgs: KnowledgeOrg[];
  projects: KnowledgeProject[];
  groups: KnowledgeGroup[];
  relations: KnowledgeRelation[];
}

/** Palette offered when creating a group, matching the app's status colours. */
export const GROUP_COLORS = [
  "var(--ac)",
  "var(--cyan)",
  "var(--green)",
  "var(--amber)",
  "var(--purple)",
  "var(--red)",
  "var(--tx3)",
];

const EMPTY_GRAPH: KnowledgeGraph = { orgs: [], projects: [], groups: [], relations: [] };

export const knowledge = {
  /** Every list is normalised: an older hostagent could answer null, and the
   *  screen maps over all four on first render. */
  async graph(): Promise<KnowledgeGraph> {
    const g = await call<Partial<KnowledgeGraph>>(HOST, "/knowledge");
    return {
      orgs: g.orgs ?? [],
      projects: g.projects ?? [],
      groups: (g.groups ?? []).map((x) => ({
        ...x,
        projects: x.projects ?? [],
        sources: x.sources ?? [],
      })),
      relations: g.relations ?? [],
    };
  },

  addOrg: (name: string) =>
    call<KnowledgeOrg>(HOST, "/knowledge/org", { method: "POST", body: JSON.stringify({ name }) }),
  renameOrg: (id: string, name: string) =>
    call<unknown>(HOST, "/knowledge/org", { method: "POST", body: JSON.stringify({ id, name }) }),
  deleteOrg: (id: string) =>
    call<unknown>(HOST, `/knowledge/org?id=${encodeURIComponent(id)}`, { method: "DELETE" }),

  addProject: (name: string, orgId: string) =>
    call<KnowledgeProject>(HOST, "/knowledge/project", {
      method: "POST",
      body: JSON.stringify({ name, orgId }),
    }),
  /** Moving replaces: a project belongs to exactly one organization. */
  moveProject: (id: string, orgId: string) =>
    call<unknown>(HOST, "/knowledge/project", { method: "POST", body: JSON.stringify({ id, orgId }) }),
  deleteProject: (id: string) =>
    call<unknown>(HOST, `/knowledge/project?id=${encodeURIComponent(id)}`, { method: "DELETE" }),

  addGroup: (name: string, color: string, owner = "", description = "") =>
    call<KnowledgeGroup>(HOST, "/knowledge/group", {
      method: "POST",
      body: JSON.stringify({ name, color, owner, description }),
    }),
  /** The id is not editable — it is the permission tag. */
  updateGroup: (g: Pick<KnowledgeGroup, "id" | "name" | "color" | "owner" | "description">) =>
    call<unknown>(HOST, "/knowledge/group", { method: "POST", body: JSON.stringify(g) }),
  deleteGroup: (id: string) =>
    call<unknown>(HOST, `/knowledge/group?id=${encodeURIComponent(id)}`, { method: "DELETE" }),

  /** Links are submitted as whole sets. Omitting a field leaves it alone;
   *  passing [] clears it — so never send [] for something you did not edit,
   *  or a group loses the sources it is allowed to retrieve. */
  setLinks: (groupId: string, links: { projects?: string[]; sources?: string[] }) =>
    call<unknown>(HOST, "/knowledge/group/links", {
      method: "POST",
      body: JSON.stringify({ groupId, ...links }),
    }),

  addRelation: (from: string, to: string, type: RelationType) =>
    call<KnowledgeRelation>(HOST, "/knowledge/relation", {
      method: "POST",
      body: JSON.stringify({ from, to, type }),
    }),
  setRelationType: (id: string, type: RelationType) =>
    call<unknown>(HOST, "/knowledge/relation", { method: "POST", body: JSON.stringify({ id, type }) }),
  deleteRelation: (id: string) =>
    call<unknown>(HOST, `/knowledge/relation?id=${encodeURIComponent(id)}`, { method: "DELETE" }),

  empty: () => EMPTY_GRAPH,
};

// --- the projected index ---

export interface GraphNeighbor {
  to: number;
  score: number;
}

export interface GraphNode {
  /** The identity: what group assignments store and what a search returns.
   *  For a local file this is the registered reference's id joined to the path
   *  within it, so two folders holding the same relative path stay two
   *  sources. */
  source: string;
  kind: "local" | "external";
  scope?: string;
  url?: string;
  /** Display name of the registered reference this came from — the folder an
   *  operator added. Older indexers omit it. */
  origin?: string;
  /** The path within that reference, which is the part a person recognises.
   *  Older indexers omit it; the source is then the whole of it. */
  rel?: string;
  groups?: string[];
  chunks: number;
  /** 0..1; the viewport decides the scale. */
  x: number;
  y: number;
  near: GraphNeighbor[];
}

export interface IndexGraph {
  nodes: GraphNode[];
  /** the projection collapsed — too few sources, or too little variation
   *  between them, to spread out. Say so rather than draw one dot. */
  degenerate: boolean;
}

// Pushing source→groups to the indexer used to live here. It does not any
// more: the host agent owns the graph and now pushes the mapping itself, at
// startup, after an edit, and before a run. Doing it from the front end made
// the permission model depend on somebody having the Knowledge screen open,
// and the indexer holds membership in memory — so a container restart, which
// registering a source deliberately causes, left it with no labels until the
// next time the screen was visited. See hostagent's knowledgesync.go.

export const indexGraph = {
  async load(): Promise<IndexGraph> {
    const g = await call<Partial<IndexGraph>>(RAG, "/graph");
    return {
      nodes: (g.nodes ?? []).map((n) => ({
        ...n,
        near: n.near ?? [],
        groups: n.groups ?? [],
        // An indexer from before sources were qualified sends neither, and its
        // paths are already the whole name — so the source stands in for both
        // and the screen groups everything under one heading.
        rel: n.rel || n.source,
        origin: n.origin ?? "",
      })),
      degenerate: g.degenerate ?? false,
    };
  },
};

/** shortLabel trims a path or URL to something that fits beside a node. */
export function shortLabel(source: string): string {
  return source.replace(/^https?:\/\//, "").split("/").slice(-2).join("/");
}
