package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"orchestra/hostagent/internal/store"
)

// TestTickRecordsExecutedOccurrence: a live tick records an 'executed' run.
func TestTickRecordsExecutedOccurrence(t *testing.T) {
	s := New(&Config{NoSeed: true})
	sc, _ := s.store.Create(&store.Schedule{Name: "t", Cron: "30 9 * * *", Active: true})

	s.tickSchedules(time.Date(2026, 7, 8, 9, 30, 0, 0, time.UTC))
	runs, _ := s.store.Runs(0)
	if len(runs) != 1 || runs[0].Status != store.RunStatusExecuted || runs[0].ScheduleID != sc.ID {
		t.Fatalf("expected 1 executed run, got %+v", runs)
	}
}

// TestBackfillMissed: occurrences whose cron passed while the app was down are
// recorded as 'missed'; a subsequent live tick at the same minute would not be
// double-counted (unique per schedule+minute).
func TestBackfillMissed(t *testing.T) {
	s := New(&Config{NoSeed: true})
	s.store.Create(&store.Schedule{Name: "daily9", Cron: "30 9 * * *", Active: true})

	// heartbeat: app was last up on the 6th at 10:00; "now" is the 9th at 11:00.
	// So the 30-9 cron fired (missed) on the 7th, 8th, 9th mornings = 3 misses.
	s.store.SetState(heartbeatKey, time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC).Format(time.RFC3339))
	s.backfillMissed(time.Date(2026, 7, 9, 11, 0, 0, 0, time.UTC))

	runs, _ := s.store.Runs(0)
	if len(runs) != 3 {
		t.Fatalf("missed backfill = %d runs, want 3: %+v", len(runs), runs)
	}
	for _, r := range runs {
		if r.Status != store.RunStatusMissed {
			t.Fatalf("expected missed, got %s", r.Status)
		}
	}
	// idempotent: re-running backfill over the same window adds nothing
	s.store.SetState(heartbeatKey, time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC).Format(time.RFC3339))
	s.backfillMissed(time.Date(2026, 7, 9, 11, 0, 0, 0, time.UTC))
	runs, _ = s.store.Runs(0)
	if len(runs) != 3 {
		t.Fatalf("backfill not idempotent: %d runs", len(runs))
	}
}

// TestBackfillFirstRunSeedsHeartbeat: with no prior heartbeat, nothing is
// backfilled (we don't know how long the app was down).
func TestBackfillFirstRunSeedsHeartbeat(t *testing.T) {
	s := New(&Config{NoSeed: true})
	s.store.Create(&store.Schedule{Name: "x", Cron: "* * * * *", Active: true})
	s.backfillMissed(time.Date(2026, 7, 9, 11, 0, 0, 0, time.UTC))
	runs, _ := s.store.Runs(0)
	if len(runs) != 0 {
		t.Fatalf("first run should backfill nothing, got %d", len(runs))
	}
	if v, _ := s.store.GetState(heartbeatKey); v == "" {
		t.Fatal("heartbeat not seeded")
	}
}

// TestRunsEndpoint exposes occurrences over HTTP.
func TestRunsEndpoint(t *testing.T) {
	srv := httptest.NewServer(New(&Config{NoSeed: true}).Handler())
	defer srv.Close()
	resp, body := req(t, srv, "GET", "/daily/runs", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if _, ok := body["runs"]; !ok {
		t.Fatalf("no runs key: %v", body)
	}
}
