package tasksource

import (
	"context"
	"testing"
)

func TestStaticFetchAppliesSinceCursor(t *testing.T) {
	s := NewStatic("demo",
		Ticket{ID: "a", UpdatedAt: "2026-07-01T00:00:00Z"},
		Ticket{ID: "b", UpdatedAt: "2026-07-05T00:00:00Z"},
	)
	got, err := s.Fetch(context.Background(), Query{Since: "2026-07-03T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("Since cursor not applied: %v", got)
	}
}

func TestRegistryLookup(t *testing.T) {
	r := NewRegistry(NewStatic("jira"), NewStatic("notion"), nil)
	if _, ok := r.Get("jira"); !ok {
		t.Fatal("jira not registered")
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("missing should not resolve")
	}
	names := r.Names()
	if len(names) != 2 || names[0] != "jira" || names[1] != "notion" {
		t.Fatalf("names = %v, want [jira notion]", names)
	}
}
