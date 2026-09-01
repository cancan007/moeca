import type { GraphNode } from "@/lib/knowledge";

// Bringing stored group membership onto the names the index now uses.
//
// A local source used to be named by its path within the folder it was found in.
// It is now named by that folder's identifier joined to the path, so that two
// registered folders holding the same relative path stay two sources. The
// indexer accepts the old name as an alias, so nothing broke — but the graph
// still holds the old spelling, and the screen compares by the new one.
//
// That mismatch is not cosmetic. A source already in a group appears unticked,
// and ticking it appends the new name beside the old: the same file, listed
// twice, counted twice, and impossible to remove in one click. Rewriting the
// stored names once puts the two halves back in agreement.
//
// An old name that now designates two files is left exactly as it is. It is the
// one case where guessing would attach a group to a file nobody chose, and the
// indexer refuses it for the same reason — a source with an ambiguous name is
// simply not granted until someone says which one they meant.

/** normalizeSources rewrites one group's stored names to the index's own.
 *
 *  Returns null when nothing needs to change, so the caller can tell a group
 *  that is already correct from one it has just corrected — and write only the
 *  ones that moved. */
export function normalizeSources(sources: string[], nodes: GraphNode[]): string[] | null {
  const known = new Set(nodes.map((n) => n.source));

  // Old names resolve through the path within a folder, which is what they were.
  // A name held by more than one node resolves to nothing: see above.
  const byRel = new Map<string, string | null>();
  for (const n of nodes) {
    const rel = n.rel || n.source;
    if (rel === n.source) continue; // unqualified already; nothing to resolve
    byRel.set(rel, byRel.has(rel) ? null : n.source);
  }

  const out: string[] = [];
  const seen = new Set<string>();
  for (const s of sources) {
    // A name the index knows is already right. An unknown one may be a legacy
    // spelling, or a file that is simply not indexed right now — a folder that
    // will come back — and that second case must survive untouched.
    const resolved = known.has(s) ? s : (byRel.get(s) ?? s);
    if (seen.has(resolved)) continue; // the same file under both spellings
    seen.add(resolved);
    out.push(resolved);
  }

  const same = out.length === sources.length && out.every((v, i) => v === sources[i]);
  return same ? null : out;
}
