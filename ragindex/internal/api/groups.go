package api

import (
	"net/http"
	"strings"

	"orchestra/ragindex/internal/index"
)

// GroupsHeader carries the groups a caller is permitted to reach — on a search,
// and on the /source route that follows what a search found. The gateway sets it
// from the run's registered session; nothing downstream of the gateway can,
// because the indexer is not on the sandbox egress network.
//
// Presence is the policy switch, and the distinction is deliberate:
//
//	header absent      → no policy; search everything (an unscoped host caller)
//	header present     → search only the named groups
//	header present, "" → search nothing
//
// The last case is the one worth being careful about. A run whose group list is
// empty must see nothing, not everything — so an empty value cannot be folded
// into "absent". Go's Header.Get returns "" for both, which is exactly the
// mistake that would turn a permission check inside out, so presence is tested
// against the header map instead.
const GroupsHeader = "X-Orchestra-Groups"

// groupFilter builds the search filter for a request, or nil when the request
// states no policy.
func groupFilter(r *http.Request) *index.GroupFilter {
	vals, ok := r.Header[http.CanonicalHeaderKey(GroupsHeader)]
	if !ok {
		return nil
	}
	return index.NewGroupFilter(parseGroups(vals))
}

// parseGroups flattens the header, accepting both repetition and comma
// separation because either is a legitimate way to send a list and callers
// should not have to know which one we prefer.
func parseGroups(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, g := range strings.Split(v, ",") {
			if g = strings.TrimSpace(g); g != "" {
				out = append(out, g)
			}
		}
	}
	return out
}
