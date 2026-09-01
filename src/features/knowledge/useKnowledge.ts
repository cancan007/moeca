import { useCallback, useEffect, useState } from "react";
import { indexGraph, knowledge, type IndexGraph, type KnowledgeGraph } from "@/lib/knowledge";
import { normalizeSources } from "./migrate";

// Loading the two halves of the Knowledge screen.
//
// They come from different services and fail independently, and that difference
// is worth surfacing rather than collapsing into one error. The authored graph
// lives in the host and is almost always available; the projected index lives
// in a container that may be down or may not have built yet. Losing the second
// means the canvas cannot be drawn, but organizations, projects and groups are
// still perfectly editable — so the screen stays usable and says which part is
// missing.

export interface KnowledgeData {
  graph: KnowledgeGraph;
  index: IndexGraph;
  /** the authored graph could not be read; nothing on the screen works. */
  graphError: string;
  /** the index could not be read; authoring still works, the canvas cannot. */
  indexError: string;
  loading: boolean;
  reload: () => void;
  /** re-read only the authored graph, after an edit. */
  reloadGraph: () => Promise<void>;
}

const EMPTY_INDEX: IndexGraph = { nodes: [], degenerate: false };

export function useKnowledge(): KnowledgeData {
  const [graph, setGraph] = useState<KnowledgeGraph>(knowledge.empty);
  const [index, setIndex] = useState<IndexGraph>(EMPTY_INDEX);
  const [graphError, setGraphError] = useState("");
  const [indexError, setIndexError] = useState("");
  const [loading, setLoading] = useState(true);

  // Reading only. Pushing membership to the indexer used to happen here, which
  // meant retrieval permissions were correct only while this screen was open —
  // a scheduled run firing overnight searched against no labels at all. The
  // host agent owns that now: it pushes at startup, after an edit, and before a
  // run. Nothing about it belongs in a screen.
  const reloadGraph = useCallback(async () => {
    try {
      setGraph(await knowledge.graph());
      setGraphError("");
    } catch (e) {
      setGraphError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const reloadIndex = useCallback(async () => {
    try {
      setIndex(await indexGraph.load());
      setIndexError("");
    } catch (e) {
      setIndexError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  // Bring stored membership onto the names the index uses, once both halves are
  // here. Local sources are now addressed by the folder they came from, and a
  // graph still holding the old spelling shows a source as unassigned when it is
  // assigned — so ticking it stores the same file twice.
  //
  // Only groups that actually move are written, and a group whose names are
  // already current writes nothing. That is what makes this safe to run on every
  // load: the second pass finds nothing to do.
  useEffect(() => {
    if (loading || index.nodes.length === 0 || graph.groups.length === 0) return;
    const stale = graph.groups
      .map((g) => ({ g, next: normalizeSources(g.sources, index.nodes) }))
      .filter((x): x is { g: KnowledgeGraph["groups"][number]; next: string[] } => x.next !== null);
    if (stale.length === 0) return;
    let cancelled = false;
    (async () => {
      for (const { g, next } of stale) {
        // Sources only: a group's project links are not part of this and must
        // not be rewritten from a list read a moment ago.
        await knowledge.setLinks(g.id, { sources: next });
      }
      if (!cancelled) await reloadGraph();
    })().catch((e) => {
      // Reported where the other graph failures are. The names stay as they
      // were, which is the state this arrived in — nothing is made worse.
      if (!cancelled) setGraphError(e instanceof Error ? e.message : String(e));
    });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, graph.groups, index.nodes]);

  const reload = useCallback(() => {
    setLoading(true);
    // Settled, not all: one service being down must not hide the other's data.
    Promise.allSettled([reloadGraph(), reloadIndex()]).then(() => setLoading(false));
  }, [reloadGraph, reloadIndex]);

  useEffect(reload, [reload]);

  return { graph, index, graphError, indexError, loading, reload, reloadGraph };
}
