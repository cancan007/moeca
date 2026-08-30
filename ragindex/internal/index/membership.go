package index

import (
	"log"
	"sync"
)

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
// Membership is also what decides a source's scope. A source nobody has put in
// a group is everyone's; one that has been assigned is reachable only through
// its groups. That is not a second policy layered on top of this mapping, it is
// a reading of it — the group already names who the knowledge is for, since it
// serves projects that belong to organizations, so asking anyone to declare the
// scope separately would be asking the same question twice.
//
// The mapping is held here and re-applied after every build, because a rebuild
// re-ingests from the indexer's own config and would otherwise silently drop
// every label — and a permission model that quietly empties itself on reindex
// would be worse than one that never worked.
//
// Three states, not two, and the third is the one worth naming:
//
//	a mapping   normal. Assignment decides who may read what.
//	empty       the host says nothing is assigned. Everything defaulted to
//	            global is everyone's, which is what an empty graph means.
//	nothing     the host has not spoken. Anything global only by default is
//	            withheld until it does — see closeUnclaimedLocked.
//
// Silence and "nothing is assigned" look alike and mean opposite things, so the
// nil map is never collapsed into an empty one.

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
// It is also persisted, so a restart does not begin blind — see groupcache.go
// for why that matters more than it used to.
func (i *Index) SetGroups(m map[string][]string) int {
	i.membership.set(m)
	i.saveGroups(m)
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.applyGroupsLocked()
}

// applyGroupsLocked re-tags chunks and sources from the stored mapping. The
// caller must hold the write lock.
func (i *Index) applyGroupsLocked() int {
	m := i.membership.get()
	if m == nil {
		i.closeUnclaimedLocked()
		return 0
	}
	// Resolve every alias a source may be named by to one canonical key, then
	// look each chunk up by the key it actually carries.
	canonical := map[string]string{}
	for _, s := range i.sources {
		canonical[sourceKey(s)] = sourceKey(s)
		if s.Path != "" {
			canonical[s.Path] = sourceKey(s)
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
	// Restated from the declared scope rather than left as found, because the
	// unclaimed state above may have cleared it while nothing was known. Once a
	// mapping exists, what is global is decided by configuration again — and
	// permits takes membership from there. Deriving both directions from
	// `declared` is what makes this reversible: an index can pass through the
	// closed state and come back out of it unchanged.
	globalFor := map[string]bool{}
	for _, s := range i.sources {
		globalFor[sourceKey(s)] = s.declared == ScopeGlobal
	}
	for k := range i.chunks {
		i.chunks[k].groups = groupsFor[i.chunks[k].Source]
		i.chunks[k].global = globalFor[i.chunks[k].Source]
	}
	for k := range i.sources {
		key := sourceKey(i.sources[k])
		i.sources[k].Groups = groupsFor[key]
		i.sources[k].Scope = effectiveScope(i.sources[k].declared, i.sources[k].Groups)
	}
	return len(matched)
}

// sourceKey is the one name a source is stored under: its URL when it has one,
// its path otherwise. Chunks carry this, and the host may address the source by
// either that or its display label — see the alias table above.
func sourceKey(s Source) string {
	if s.URL != "" {
		return s.URL
	}
	return s.Path
}

// closeUnclaimedLocked hides everything that is global only by default, for as
// long as nothing has been pushed at all.
//
// The host has not spoken yet, so this process does not know who any source is
// for — and since scope is read off membership, "no labels" would otherwise
// read as "everything is everyone's". That is the wrong way to be wrong. A
// mapping is normally in place well before this matters: it is restored from
// disk at startup, and the host agent pushes at its own startup and again
// before every run. This is what happens when all of that has failed.
//
// The distinction it turns on is whether a source's global scope was DECLARED
// or merely defaulted to. Declaring it is a person saying the knowledge is
// everyone's, and this is not the place to overrule that — it is also the only
// way to run this indexer standalone, with sources and groups written straight
// into its config and no host to push anything. Defaulting to it is nobody
// having said anything, which is exactly the state that should not grant.
//
// Only callers WITH a policy are affected. An unscoped search carries a nil
// filter, which never consults this at all, so the host's own routes and every
// run that asked for no scope behave as they always did.
func (i *Index) closeUnclaimedLocked() {
	unclaimed := map[string]bool{}
	for k := range i.sources {
		if !i.sources[k].assumedGlobal {
			continue
		}
		unclaimed[sourceKey(i.sources[k])] = true
		// Reported as restricted, because it is: a scoped search skips it. The
		// panel saying "global · default" about something no run can reach is
		// the same error as the filter being wrong, printed instead of applied.
		i.sources[k].Scope = ScopeProject
	}
	if len(unclaimed) == 0 {
		return
	}
	for k := range i.chunks {
		if unclaimed[i.chunks[k].Source] {
			i.chunks[k].global = false
		}
	}
	log.Printf("ragindex: no group membership has been pushed; %d source(s) global only by default are hidden from scoped searches until it arrives", len(unclaimed))
}

// effectiveScope is the scope a source is actually reachable under, given what
// it was configured with and which groups it ended up in.
//
// The filter itself derives this from membership at query time — see permits —
// so this exists for the UI, which lists sources by scope and would otherwise
// badge a source as everyone's while every scoped search was skipping it. A
// panel that says "global · default" about a source nobody outside one team can
// reach is not a smaller problem than the filter being wrong; it is the same
// problem, reported as fact.
//
// A source in a group reports "project" rather than "organization" because the
// two are indistinguishable to the filter — only global is exempt — and of the
// two, project is the narrower reading. Guessing the broader one from a group
// whose reach this package cannot see would overstate what was granted.
//
// Narrowing only: a source declared narrower than global stays there whether or
// not it is in a group, because that declaration is a person's decision and
// membership is not the authority to undo it.
func effectiveScope(declared string, groups []string) string {
	if declared == ScopeGlobal && len(groups) > 0 {
		return ScopeProject
	}
	return declared
}
