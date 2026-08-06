import type { GraphNode } from "@/lib/knowledge";

// Choosing which similarity edges to draw.
//
// The indexer sends a fixed depth of nearest neighbours per node, and the three
// controls on screen are filters over that. Keeping them client-side means the
// sliders are instant and moving one never refetches the index.
//
// Every node has a nearest neighbour, so drawing all of them says nothing — a
// uniformly noisy index looks exactly like a well-clustered one. The controls
// exist to find the level at which structure appears, which is why the count of
// drawn edges is shown next to them.

export interface Edge {
  a: number;
  b: number;
  score: number;
}

export interface EdgeOptions {
  /** how many neighbours per node to consider. */
  topN: number;
  /** cosine floor, 0..1. */
  threshold: number;
  /** keep only pairs that name each other. */
  mutual: boolean;
  /** node indices currently eligible (type filters); others are skipped. */
  visible?: (i: number) => boolean;
}

/** selectEdges reduces the per-node neighbour lists to a deduplicated edge set.
 *
 *  Pairs are emitted once, keyed low-to-high, because an undrawn duplicate
 *  still costs a DOM node and a doubled stroke reads as a stronger link. */
export function selectEdges(nodes: GraphNode[], opts: EdgeOptions): Edge[] {
  const { topN, threshold, mutual, visible } = opts;
  const shown = visible ?? (() => true);

  // Who each node claims as a near neighbour, at this depth.
  const claims: Set<number>[] = nodes.map((n, i) => {
    const s = new Set<number>();
    if (!shown(i)) return s;
    let taken = 0;
    for (const nb of n.near) {
      if (taken >= topN) break;
      if (!shown(nb.to) || nb.score < threshold) continue;
      s.add(nb.to);
      taken++;
    }
    return s;
  });

  const seen = new Set<string>();
  const out: Edge[] = [];
  nodes.forEach((n, i) => {
    if (!shown(i)) return;
    let taken = 0;
    for (const nb of n.near) {
      if (taken >= topN) break;
      if (!shown(nb.to) || nb.score < threshold) continue;
      taken++;
      // Mutual mode keeps only pairs that agree they are close. It is the
      // honest view: a hub document is everyone's neighbour without anything
      // being especially near it, and one-directional edges make that hub look
      // like the centre of a cluster that is not there.
      if (mutual && !claims[nb.to].has(i)) continue;
      const key = i < nb.to ? `${i}-${nb.to}` : `${nb.to}-${i}`;
      if (seen.has(key)) continue;
      seen.add(key);
      out.push({ a: i, b: nb.to, score: nb.score });
    }
  });
  return out;
}

/** totalPairs is how many edges would exist with no filtering at all, so the
 *  panel can show what fraction is being drawn. */
export function totalPairs(nodes: GraphNode[], visible?: (i: number) => boolean): number {
  const shown = visible ?? (() => true);
  let n = 0;
  for (let i = 0; i < nodes.length; i++) if (shown(i)) n++;
  return (n * (n - 1)) / 2;
}

/** strokeWidth maps a cosine score onto a visible weight. Similarities cluster
 *  in a narrow band near the top, so the scale starts at the floor being drawn
 *  rather than at zero — otherwise every edge renders the same width. */
export function strokeWidth(score: number, threshold: number): number {
  const span = Math.max(0.02, 1 - threshold);
  const t = Math.min(1, Math.max(0, (score - threshold) / span));
  return 0.35 + t * 2.6;
}
