package api

import (
	"net/http/httptest"
	"testing"
	"time"

	"orchestra/hostagent/internal/store"
)

func httptestNewSchedulesServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(New(&Config{}).Handler())
}

func TestCronMatches(t *testing.T) {
	// 2026-07-08 is a Wednesday (weekday 3).
	wed := time.Date(2026, 7, 8, 9, 30, 0, 0, time.UTC)

	cases := []struct {
		expr string
		t    time.Time
		want bool
	}{
		// all wildcards
		{"* * * * *", wed, true},
		// exact minute + hour
		{"30 9 * * *", wed, true},
		{"31 9 * * *", wed, false},
		{"30 8 * * *", wed, false},
		// step */n on minute (30 % 15 == 0)
		{"*/15 * * * *", wed, true},
		{"*/7 * * * *", wed, false},
		// comma list
		{"0,30 * * * *", wed, true},
		{"0,15,45 * * * *", wed, false},
		// day-of-month field
		{"* * 8 * *", wed, true},
		{"* * 9 * *", wed, false},
		// month field (July == 7)
		{"* * * 7 *", wed, true},
		{"* * * 6 *", wed, false},
		// day-of-week field (Wed == 3)
		{"* * * * 3", wed, true},
		{"* * * * 0", wed, false},
		{"* * * * 1,3,5", wed, true},
		// unsupported range => no match, no crash
		{"0 9 * * 1-5", wed, false},
		// wrong field count
		{"* * *", wed, false},
		{"", wed, false},
	}
	for _, c := range cases {
		if got := cronMatches(c.expr, c.t); got != c.want {
			t.Errorf("cronMatches(%q, %v) = %v, want %v", c.expr, c.t.Format("15:04 Mon 02 Jan"), got, c.want)
		}
	}
}

func TestTickSchedulesRecordsRun(t *testing.T) {
	s := New(&Config{NoSeed: true})
	// create an active schedule whose cron matches the tick below
	created, err := s.store.Create(&store.Schedule{Name: "t", Cron: "30 9 * * *", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	find := func() *store.Schedule {
		list, _ := s.store.List()
		for _, x := range list {
			if x.ID == id {
				return x
			}
		}
		t.Fatalf("schedule %s not found", id)
		return nil
	}

	when := time.Date(2026, 7, 8, 9, 30, 0, 0, time.UTC)
	s.tickSchedules(when)
	if got := find(); got.RunCount != 1 {
		t.Fatalf("runCount = %d, want 1", got.RunCount)
	}
	if find().LastRun == "" {
		t.Fatal("lastRun not set")
	}

	// same minute again => fire-once guard prevents a second run
	s.tickSchedules(when)
	if got := find(); got.RunCount != 1 {
		t.Fatalf("runCount after re-tick same minute = %d, want 1", got.RunCount)
	}

	// next minute, no match => unchanged
	s.tickSchedules(when.Add(time.Minute))
	if got := find(); got.RunCount != 1 {
		t.Fatalf("runCount after non-matching tick = %d, want 1", got.RunCount)
	}
}

func TestScheduleCRUD(t *testing.T) {
	srv := httptestNewSchedulesServer(t)
	defer srv.Close()

	// list seeded
	resp, body := req(t, srv, "GET", "/schedules", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("list: status %d", resp.StatusCode)
	}
	seeded, _ := body["schedules"].([]any)
	if len(seeded) < 2 {
		t.Fatalf("seeded schedules = %d, want >= 2", len(seeded))
	}

	// create
	resp, body = req(t, srv, "POST", "/schedules", map[string]any{
		"name": "テスト", "cron": "0 12 * * *", "perspective": "automation", "task": "do", "active": true,
	})
	if resp.StatusCode != 201 {
		t.Fatalf("create: status %d (%v)", resp.StatusCode, body)
	}
	id, _ := body["id"].(string)
	if id == "" || body["name"] != "テスト" || body["active"] != true {
		t.Fatalf("create returned %v", body)
	}

	// list now has one more
	_, body = req(t, srv, "GET", "/schedules", nil)
	after, _ := body["schedules"].([]any)
	if len(after) != len(seeded)+1 {
		t.Fatalf("after create list = %d, want %d", len(after), len(seeded)+1)
	}

	// toggle -> active flips to false
	resp, body = req(t, srv, "POST", "/schedules/toggle", map[string]string{"id": id})
	if resp.StatusCode != 200 || body["active"] != false {
		t.Fatalf("toggle: status %d body %v", resp.StatusCode, body)
	}

	// delete
	resp, body = req(t, srv, "DELETE", "/schedules?id="+id, nil)
	if resp.StatusCode != 200 || body["removed"] != id {
		t.Fatalf("delete: status %d body %v", resp.StatusCode, body)
	}

	// deleting again -> 404
	resp, _ = req(t, srv, "DELETE", "/schedules?id="+id, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("delete missing: status %d, want 404", resp.StatusCode)
	}

	// back to seeded count
	_, body = req(t, srv, "GET", "/schedules", nil)
	final, _ := body["schedules"].([]any)
	if len(final) != len(seeded) {
		t.Fatalf("final list = %d, want %d", len(final), len(seeded))
	}
}

func TestScheduleGoalRequiresMilestone(t *testing.T) {
	srv := httptestNewSchedulesServer(t)
	defer srv.Close()

	// goal without milestones => 400
	resp, _ := req(t, srv, "POST", "/schedules", map[string]any{
		"name": "g", "cron": "0 9 * * *", "goal": "コスト削減",
	})
	if resp.StatusCode != 400 {
		t.Fatalf("goal w/o milestone: status %d, want 400", resp.StatusCode)
	}

	// goal + a milestone => 201, persisted and returned
	resp, body := req(t, srv, "POST", "/schedules", map[string]any{
		"name": "g", "cron": "0 9 * * *", "goal": "コスト削減",
		"milestones": []map[string]any{{"title": "M1: 現状把握", "done": false}},
	})
	if resp.StatusCode != 201 {
		t.Fatalf("goal + milestone: status %d (%v)", resp.StatusCode, body)
	}
	_, body = req(t, srv, "GET", "/schedules", nil)
	found := false
	for _, s := range body["schedules"].([]any) {
		m := s.(map[string]any)
		if m["goal"] == "コスト削減" {
			if ms, _ := m["milestones"].([]any); len(ms) == 1 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("goal/milestones not persisted or returned")
	}

	// no goal => milestones not required
	resp, _ = req(t, srv, "POST", "/schedules", map[string]any{"name": "n", "cron": "0 9 * * *"})
	if resp.StatusCode != 201 {
		t.Fatalf("no-goal create: status %d, want 201", resp.StatusCode)
	}
}
