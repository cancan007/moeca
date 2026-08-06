import { describe, expect, it } from "vitest";
import type { GraphNode } from "@/lib/knowledge";
import { selectEdges, strokeWidth, totalPairs } from "./edges";

function node(near: [number, number][]): GraphNode {
  return {
    source: "s",
    kind: "local",
    chunks: 1,
    x: 0,
    y: 0,
    near: near.map(([to, score]) => ({ to, score })),
  };
}

describe("selectEdges", () => {
  // A doubled stroke reads as a stronger link, so a pair named from both ends
  // must still be one edge.
  it("emits each pair once", () => {
    const nodes = [node([[1, 0.9]]), node([[0, 0.9]])];
    const edges = selectEdges(nodes, { topN: 3, threshold: 0.5, mutual: false });
    expect(edges).toHaveLength(1);
  });

  it("drops neighbours below the threshold", () => {
    const nodes = [node([[1, 0.9], [2, 0.4]]), node([[0, 0.9]]), node([[0, 0.4]])];
    const edges = selectEdges(nodes, { topN: 5, threshold: 0.8, mutual: false });
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({ a: 0, b: 1 });
  });

  // topN counts neighbours considered per node, taken in the order the indexer
  // ranked them.
  it("respects topN per node", () => {
    const nodes = [node([[1, 0.9], [2, 0.85], [3, 0.8]]), node([]), node([]), node([])];
    expect(selectEdges(nodes, { topN: 1, threshold: 0, mutual: false })).toHaveLength(1);
    expect(selectEdges(nodes, { topN: 3, threshold: 0, mutual: false })).toHaveLength(3);
  });

  // The point of mutual mode: a hub that everyone names, but which names none
  // of them back, should not appear as the centre of a cluster.
  it("mutual mode keeps only pairs that name each other", () => {
    const hub = node([[3, 0.95]]); // index 0 — its own nearest is elsewhere
    const nodes = [hub, node([[0, 0.9]]), node([[0, 0.9]]), node([[0, 0.95]])];
    const loose = selectEdges(nodes, { topN: 1, threshold: 0, mutual: false });
    expect(loose).toHaveLength(3);
    const strict = selectEdges(nodes, { topN: 1, threshold: 0, mutual: true });
    expect(strict).toHaveLength(1);
    expect(strict[0]).toMatchObject({ a: 0, b: 3 });
  });

  // Hiding a node type must hide its edges too, not leave them dangling.
  it("skips edges touching a hidden node", () => {
    const nodes = [node([[1, 0.9], [2, 0.9]]), node([[0, 0.9]]), node([[0, 0.9]])];
    const edges = selectEdges(nodes, {
      topN: 5,
      threshold: 0,
      mutual: false,
      visible: (i) => i !== 2,
    });
    expect(edges).toHaveLength(1);
    expect(edges[0]).toMatchObject({ a: 0, b: 1 });
  });

  it("returns nothing for an empty graph", () => {
    expect(selectEdges([], { topN: 3, threshold: 0.5, mutual: true })).toEqual([]);
  });
});

describe("totalPairs", () => {
  it("counts only visible nodes", () => {
    const nodes = [node([]), node([]), node([]), node([])];
    expect(totalPairs(nodes)).toBe(6);
    expect(totalPairs(nodes, (i) => i < 2)).toBe(1);
  });
});

describe("strokeWidth", () => {
  // Scores cluster near the top, so the scale has to start at the floor being
  // drawn or every edge comes out the same width.
  it("spreads weight across the visible band", () => {
    const atFloor = strokeWidth(0.7, 0.7);
    const above = strokeWidth(0.85, 0.7);
    expect(above).toBeGreaterThan(atFloor + 0.5);
  });

  it("clamps outside the band", () => {
    expect(strokeWidth(0.1, 0.7)).toBe(strokeWidth(0.7, 0.7));
    expect(strokeWidth(1.4, 0.7)).toBe(strokeWidth(1, 0.7));
  });
});
