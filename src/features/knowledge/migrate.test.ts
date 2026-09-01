import { describe, expect, it } from "vitest";
import type { GraphNode } from "@/lib/knowledge";
import { normalizeSources } from "./migrate";

const node = (source: string, rel: string): GraphNode =>
  ({ source, rel, kind: "local", chunks: 1, x: 0, y: 0, near: [] }) as GraphNode;

const nodes = [
  node("moeca_rag-881f/kon/character-bible-kon.md", "kon/character-bible-kon.md"),
  node("moeca_rag-881f/kon/images/dog_sitting.JPEG", "kon/images/dog_sitting.JPEG"),
];

describe("normalizeSources", () => {
  it("rewrites an old name to the one the index uses", () => {
    expect(normalizeSources(["kon/character-bible-kon.md"], nodes)).toEqual([
      "moeca_rag-881f/kon/character-bible-kon.md",
    ]);
  });

  // The actual symptom: ticking a source already stored under its old name
  // appended the new one beside it. The same file, listed twice.
  it("collapses a file stored under both spellings", () => {
    const got = normalizeSources(
      ["kon/images/dog_sitting.JPEG", "moeca_rag-881f/kon/images/dog_sitting.JPEG"],
      nodes,
    );
    expect(got).toEqual(["moeca_rag-881f/kon/images/dog_sitting.JPEG"]);
  });

  it("says nothing to do when the names are already current", () => {
    expect(normalizeSources(["moeca_rag-881f/kon/images/dog_sitting.JPEG"], nodes)).toBeNull();
  });

  // Guessing here would attach a group to a file nobody chose.
  it("leaves an old name that now designates two files alone", () => {
    const ambiguous = [
      node("team-a-1111/README.md", "README.md"),
      node("team-b-2222/README.md", "README.md"),
    ];
    expect(normalizeSources(["README.md"], ambiguous)).toBeNull();
  });

  // A folder that is unregistered today may be registered again tomorrow, and
  // its assignments should still be there when it is.
  it("keeps a name the index does not have", () => {
    expect(normalizeSources(["removed/for/now.md"], nodes)).toBeNull();
  });

  it("preserves order", () => {
    const got = normalizeSources(
      ["kon/images/dog_sitting.JPEG", "kon/character-bible-kon.md"],
      nodes,
    );
    expect(got).toEqual([
      "moeca_rag-881f/kon/images/dog_sitting.JPEG",
      "moeca_rag-881f/kon/character-bible-kon.md",
    ]);
  });
});
