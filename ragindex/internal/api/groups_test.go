package api

import (
	"net/http"
	"reflect"
	"testing"
)

// The three-way distinction between absent, populated and empty is the whole
// policy switch, so it is asserted directly rather than through a search.
func TestGroupFilterFromHeader(t *testing.T) {
	req := func(vals ...string) *http.Request {
		r, _ := http.NewRequest("POST", "/search", nil)
		for _, v := range vals {
			r.Header.Add(GroupsHeader, v)
		}
		return r
	}

	if f := groupFilter(req()); f != nil {
		t.Error("an absent header must state no policy (nil filter)")
	}
	// An empty header value is a run with no groups. It must produce a filter
	// that permits nothing — not nil, which would permit everything.
	f := groupFilter(req(""))
	if f == nil {
		t.Fatal("an empty header must still state a policy")
	}
	if len(f.Groups()) != 0 {
		t.Errorf("Groups() = %v, want none", f.Groups())
	}
	if got := groupFilter(req("a,b")).Groups(); len(got) != 2 {
		t.Errorf("comma-separated header gave %v, want two groups", got)
	}
	if got := groupFilter(req("a", "b")).Groups(); len(got) != 2 {
		t.Errorf("repeated header gave %v, want two groups", got)
	}
}

func TestParseGroups(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want []string
	}{
		{[]string{"a, b ,c"}, []string{"a", "b", "c"}},
		{[]string{"a", "b"}, []string{"a", "b"}},
		{[]string{" , "}, nil},
		{[]string{""}, nil},
	} {
		if got := parseGroups(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseGroups(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
