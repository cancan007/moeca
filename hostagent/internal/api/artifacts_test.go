package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"orchestra/hostagent/internal/store"
)

// firedSchedule runs one schedule and returns the serving host agent, the HTTP
// server in front of it, the occurrence id and the directory the run wrote to.
func firedSchedule(t *testing.T) (*httptest.Server, int64, string) {
	t.Helper()
	s, _ := dailyServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte(`{"runId":"r1"}`))
	})
	s.store.Create(&store.Schedule{
		Name: "daily", Cron: "0 3 * * *", Active: true,
		RunSpec: []byte(`{"stages":[{"id":"a"}]}`),
	})
	s.tickSchedules(time.Date(2026, 7, 8, 3, 0, 0, 0, time.UTC))

	runs, _ := s.store.Runs(0)
	if len(runs) != 1 || runs[0].OutputDir == "" {
		t.Fatalf("expected one occurrence with an output dir, got %+v", runs)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, runs[0].ID, runs[0].OutputDir
}

func getRaw(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func TestDailyArtifacts_ListsWhatTheRunProduced(t *testing.T) {
	srv, runID, dir := firedSchedule(t)

	os.WriteFile(filepath.Join(dir, "summary.mp4"), []byte("video-bytes"), 0o644)
	os.WriteFile(filepath.Join(dir, "report.md"), []byte("# hi"), 0o644)
	os.MkdirAll(filepath.Join(dir, "charts"), 0o755)
	os.WriteFile(filepath.Join(dir, "charts", "hits.png"), []byte("png"), 0o644)
	// Orchestra's own bookkeeping is not a deliverable.
	os.MkdirAll(filepath.Join(dir, ".orchestra", "delegate"), 0o755)
	os.WriteFile(filepath.Join(dir, ".orchestra", "delegate", "request.json"), []byte("{}"), 0o644)

	res, body := getRaw(t, srv.URL+"/daily/artifacts?run="+itoa(runID))
	if res.StatusCode != 200 {
		t.Fatalf("status = %d: %s", res.StatusCode, body)
	}
	var out struct{ Artifacts []Artifact }
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}

	kinds := map[string]string{}
	for _, a := range out.Artifacts {
		kinds[a.Path] = a.Kind
	}
	if len(out.Artifacts) != 3 {
		t.Fatalf("artifacts = %+v, want 3 (the .orchestra channel is not output)", out.Artifacts)
	}
	if kinds["summary.mp4"] != "video" || kinds["report.md"] != "text" || kinds["charts/hits.png"] != "image" {
		t.Errorf("kinds = %v", kinds)
	}
	for _, a := range out.Artifacts {
		if a.Size == 0 || a.Name == "" || a.ModTime == "" {
			t.Errorf("incomplete artifact: %+v", a)
		}
	}
}

// A gallery that corrupts what it shows is worse than none: reading bytes back
// through a JSON string mangles anything binary.
func TestDailyArtifact_ServesBytesIntactWithRanges(t *testing.T) {
	srv, runID, dir := firedSchedule(t)
	want := []byte{0x00, 0x01, 0xff, 0xfe, 0x89, 'P', 'N', 'G'}
	os.WriteFile(filepath.Join(dir, "chart.png"), want, 0o644)

	res, got := getRaw(t, srv.URL+"/daily/artifact?run="+itoa(runID)+"&path=chart.png")
	if res.StatusCode != 200 {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if string(got) != string(want) {
		t.Errorf("bytes changed in transit: got % x, want % x", got, want)
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}

	// Range support: without it a <video> plays from the start but cannot seek.
	os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("0123456789"), 0o644)
	r, _ := http.NewRequest("GET", srv.URL+"/daily/artifact?run="+itoa(runID)+"&path=clip.mp4", nil)
	r.Header.Set("Range", "bytes=2-5")
	rangeRes, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer rangeRes.Body.Close()
	part, _ := io.ReadAll(rangeRes.Body)
	if rangeRes.StatusCode != http.StatusPartialContent || string(part) != "2345" {
		t.Errorf("range: status %d body %q, want 206 \"2345\"", rangeRes.StatusCode, part)
	}
}

// The load-bearing one. An agent decides what it writes, so a run can produce
// an .html or .svg. Rendering one inline would execute its script in the UI's
// own origin — the origin that talks to the loopback services. Only media may
// be inline; everything else is an inert download.
func TestDailyArtifact_OnlyMediaIsServedInline(t *testing.T) {
	srv, runID, dir := firedSchedule(t)

	for name, content := range map[string]string{
		"report.html": "<script>fetch('/task/merge',{method:'POST'})</script>",
		"chart.svg":   `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"notes.md":    "# hello",
		"run.js":      "alert(1)",
	} {
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
		res, _ := getRaw(t, srv.URL+"/daily/artifact?run="+itoa(runID)+"&path="+name)
		if ct := res.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("%s: Content-Type = %q, want application/octet-stream", name, ct)
		}
		if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
			t.Errorf("%s: Content-Disposition = %q, want an attachment", name, cd)
		}
		if res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff — a sniffed type reintroduces the whole problem", name)
		}
	}
}

func TestDailyArtifact_DownloadForcesAnAttachment(t *testing.T) {
	srv, runID, dir := firedSchedule(t)
	os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("x"), 0o644)

	res, _ := getRaw(t, srv.URL+"/daily/artifact?run="+itoa(runID)+"&path=clip.mp4&download=1")
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="clip.mp4"`) {
		t.Errorf("Content-Disposition = %q, want it to name the file", cd)
	}
}

// The output directory is written by an agent, so it can contain a symlink
// pointing anywhere on the host. A path check alone passes such a link — it is
// an ordinary relative path — and then the file server follows it.
func TestDailyArtifact_RefusesEscapes(t *testing.T) {
	srv, runID, dir := firedSchedule(t)

	secret := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600)
	if err := os.Symlink(secret, filepath.Join(dir, "innocuous.png")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, p := range []string{"innocuous.png", "../../../etc/passwd", "/etc/passwd", "..%2F..%2Fetc%2Fpasswd", ""} {
		res, body := getRaw(t, srv.URL+"/daily/artifact?run="+itoa(runID)+"&path="+p)
		if res.StatusCode == 200 {
			t.Errorf("path %q was served: %s", p, body)
		}
		if strings.Contains(string(body), "PRIVATE KEY") {
			t.Errorf("path %q leaked the linked file", p)
		}
	}
}

// A caller supplies an occurrence id, never a directory: the mapping from id to
// path happens here, so the reachable set is exactly the directories this app
// created for its own runs.
func TestDailyArtifacts_UnknownRunIsRefused(t *testing.T) {
	srv, _, _ := firedSchedule(t)
	for _, q := range []string{"run=999999", "run=", "run=abc", "run=../../etc"} {
		if res, _ := getRaw(t, srv.URL+"/daily/artifacts?"+q); res.StatusCode != 404 {
			t.Errorf("%s: status = %d, want 404", q, res.StatusCode)
		}
		if res, _ := getRaw(t, srv.URL+"/daily/artifact?"+q+"&path=x"); res.StatusCode != 404 {
			t.Errorf("%s: artifact status = %d, want 404", q, res.StatusCode)
		}
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
