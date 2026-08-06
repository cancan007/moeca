package store

import (
	"path/filepath"
	"testing"

	"orchestra/hostagent/internal/tasksource"
)

func TestSchedulePersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(&Schedule{Name: "x", Cron: "* * * * *", Active: true}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	list, err := st2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "x" {
		t.Fatalf("after reopen = %v, want 1 schedule 'x'", list)
	}
}

// TestSeedOnceNotResurrectedAfterDelete pins the bug fix: deleting all
// schedules must NOT reseed examples on the next open.
func TestSeedOnceNotResurrectedAfterDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	ex := []*Schedule{{Name: "a", Cron: "* * * * *"}}

	st, _ := Open(path)
	if err := st.SeedOnce(ex); err != nil {
		t.Fatal(err)
	}
	if err := st.SeedOnce(ex); err != nil { // idempotent within a session
		t.Fatal(err)
	}
	list, _ := st.List()
	if len(list) != 1 {
		t.Fatalf("seeded %d times, want 1", len(list))
	}
	if _, err := st.Delete(list[0].ID); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st2, _ := Open(path)
	defer st2.Close()
	if err := st2.SeedOnce(ex); err != nil {
		t.Fatal(err)
	}
	l2, _ := st2.List()
	if len(l2) != 0 {
		t.Fatalf("reseeded after delete: %d schedules, want 0", len(l2))
	}
}

func TestUpsertTicketsDedupesByID(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()

	first := []tasksource.Ticket{{ID: "jira:1", Source: "jira", Title: "old", State: "open", UpdatedAt: "2026-07-01T00:00:00Z"}}
	if err := st.UpsertTickets(first); err != nil {
		t.Fatal(err)
	}
	second := []tasksource.Ticket{{ID: "jira:1", Source: "jira", Title: "new", State: "closed", UpdatedAt: "2026-07-02T00:00:00Z"}}
	if err := st.UpsertTickets(second); err != nil {
		t.Fatal(err)
	}

	got, err := st.Tickets("jira")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("tickets = %d, want 1 (upsert by id)", len(got))
	}
	if got[0].Title != "new" || got[0].State != "closed" {
		t.Fatalf("ticket not updated: %+v", got[0])
	}
}
