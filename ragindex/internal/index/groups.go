package index

import "strings"

// Group-scoped retrieval.
//
// A group is a label attached to a knowledge source; every chunk derived from
// that source inherits it. A search may carry a set of permitted groups, and
// only chunks tagged with at least one of them are considered.
//
// This is the enforcement point for per-task knowledge permissions. The caller
// (the gateway) decides which groups a run may see and states them in a header;
// this package trusts that statement, which is sound only because the indexer
// is not reachable from the sandbox network — every request arrives through the
// gateway, so there is no path by which an agent could set the header itself.
// If ragindex is ever exposed directly, this becomes advisory and the filter
// must move behind a credential.
//
// Matching is exact after trimming surrounding whitespace: a mismatch — a typo,
// a case difference — denies rather than grants, which is the safe direction
// for a permission check.

// GroupFilter is a set of permitted groups. A nil filter states no policy and
// permits everything, which is what an unscoped caller gets.
type GroupFilter struct {
	allow map[string]struct{}
}

// NewGroupFilter builds a filter permitting exactly the named groups. Note the
// difference between a nil filter and an empty one: nil means "no policy" and
// permits all chunks, while NewGroupFilter(nil) permits none. Callers must not
// collapse a missing header into an empty list, or an unscoped request would
// silently return nothing.
func NewGroupFilter(groups []string) *GroupFilter {
	allow := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		if g = strings.TrimSpace(g); g != "" {
			allow[g] = struct{}{}
		}
	}
	return &GroupFilter{allow: allow}
}

// Groups lists the permitted groups. Order is unspecified; this exists for
// diagnostics, not for logic.
func (f *GroupFilter) Groups() []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.allow))
	for g := range f.allow {
		out = append(out, g)
	}
	return out
}

// permits reports whether a chunk may be returned.
//
// A globally-scoped chunk always may. That is the point of the scope: knowledge
// declared as everyone's — a handbook, a glossary, a coding standard — should
// not need a group membership per team to be readable, and requiring one would
// mean re-granting the same document to every group forever.
//
// Otherwise an untagged chunk is visible only when there is no policy at all.
// Under a policy it is hidden, because "no group" cannot be shown to belong to
// any permitted one. Note that this is a decision about a NARROWLY-scoped
// source with no labels — the safe reading of an incomplete assignment — and is
// not the same question as the default scope, which is global precisely so that
// nothing lands in that state by accident.
func (f *GroupFilter) permits(groups []string, global bool) bool {
	if f == nil || global {
		return true
	}
	for _, g := range groups {
		if _, ok := f.allow[g]; ok {
			return true
		}
	}
	return false
}

// normalizeGroups trims and de-duplicates a source's declared groups, dropping
// empties. The result is shared by every chunk of that source, so it is built
// once at ingest rather than per chunk.
func normalizeGroups(groups []string) []string {
	if len(groups) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(groups))
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if _, dup := seen[g]; dup {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
