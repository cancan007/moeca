package index

import "sync"

// Group membership pushed from the host.
//
// The host owns the Knowledge graph — which sources belong to which group — so
// the indexer is told rather than asked. It cannot ask: the host agent is not
// reachable from the container network, which is the same arrangement that
// keeps the indexer itself off the sandbox network.
//
// Applying a membership change does not re-embed anything. Group labels are
// metadata hanging off chunks that are already built, so a permission edit
// takes effect immediately instead of waiting on the whole index to rebuild —
// which matters most in the direction that removes access.
//
// The mapping is held here and re-applied after every build, because a rebuild
// re-ingests from the indexer's own config and would otherwise silently drop
// every label. Dropping them fails closed — an untagged source is invisible to
// any group-scoped search — but a permission model that quietly empties itself
// on reindex would be worse than one that never worked.

// membership is the host-pushed source→groups mapping.
type membership struct {
	mu sync.RWMutex
	m  map[string][]string
}

func (b *membership) set(m map[string][]string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m = m
}

func (b *membership) get() map[string][]string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.m
}

// SetGroups replaces the source→groups mapping and applies it to the live
// index. It reports how many sources in the current index were matched, so the
// caller can tell a successful push from one whose keys mean nothing here.
//
// Keys may be either the path/URL a chunk is stored under or the display label
// a named external source reports; both are accepted because the screen shows
// the label and the index stores the URL, and making the caller know the
// difference would be a trap.
func (i *Index) SetGroups(m map[string][]string) int {
	i.membership.set(m)
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.applyGroupsLocked()
}

// applyGroupsLocked re-tags chunks and sources from the stored mapping. The
// caller must hold the write lock.
func (i *Index) applyGroupsLocked() int {
	m := i.membership.get()
	if m == nil {
		return 0
	}
	// Resolve every alias a source may be named by to one canonical key, then
	// look each chunk up by the key it actually carries.
	canonical := map[string]string{}
	for _, s := range i.sources {
		key := s.Path
		if s.URL != "" {
			key = s.URL
		}
		canonical[key] = key
		if s.Path != "" {
			canonical[s.Path] = key
		}
	}
	groupsFor := map[string][]string{}
	matched := map[string]bool{}
	for alias, groups := range m {
		key, ok := canonical[alias]
		if !ok {
			// A source the index does not have. Keeping the mapping is right —
			// the file may come back on the next build — but nothing to tag now.
			continue
		}
		groupsFor[key] = normalizeGroups(groups)
		matched[key] = true
	}
	for k := range i.chunks {
		i.chunks[k].groups = groupsFor[i.chunks[k].Source]
	}
	for k := range i.sources {
		key := i.sources[k].Path
		if i.sources[k].URL != "" {
			key = i.sources[k].URL
		}
		i.sources[k].Groups = groupsFor[key]
	}
	return len(matched)
}
