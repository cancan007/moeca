package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"orchestra/hostagent/internal/store"
)

// A submitted filename is attacker-controlled in the same sense every path an
// agent chooses is, so it gets the same treatment: reduced to a leaf that
// cannot name anywhere else.
func TestAttachNameCannotEscape(t *testing.T) {
	// Names that are not names at all are refused outright.
	for _, bad := range []string{"", "   ", ".", "..", ".hidden", "/"} {
		if got, err := attachName(bad); err == nil {
			t.Errorf("attachName(%q) = %q, want an error", bad, got)
		}
	}
	// A hostile path is not refused, it is contained: what comes back is a leaf
	// that names a file in the attachment directory and cannot name anywhere
	// else. Asserting the property rather than the exact string, since the point
	// is where the result can point, not what it is called.
	for _, hostile := range []string{"../../etc/passwd", "/etc/passwd", `..\..\windows\system32`, "../../../../root/.ssh/id_rsa"} {
		got, err := attachName(hostile)
		if err != nil {
			continue // refusing is also fine
		}
		if strings.ContainsAny(got, `/\`) || got == "." || got == ".." || filepath.IsAbs(got) {
			t.Errorf("attachName(%q) = %q, which can still name another directory", hostile, got)
		}
	}
	for in, want := range map[string]string{
		"ref.png":            "ref.png",
		"  spaced.csv  ":     "spaced.csv",
		"dir/inner/ref.jpg":  "ref.jpg",
		`dir\inner\ref.jpg`:  "ref.jpg",
		"../../../still.png": "still.png",
	} {
		got, err := attachName(in)
		if err != nil || got != want {
			t.Errorf("attachName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func attachServer(t *testing.T) (*Server, *httptest.Server, *store.Schedule) {
	t.Helper()
	s := New(&Config{NoSeed: true, DataDir: t.TempDir()})
	sc, err := s.store.Create(&store.Schedule{Name: "daily", Cron: "0 9 * * *", Perspective: "automation", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv, sc
}

func upload(t *testing.T, srv *httptest.Server, schedule, name, body string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte(body))
	w.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/daily/attachment?schedule="+schedule, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAttachmentRoundTripAndStaging(t *testing.T) {
	s, srv, sc := attachServer(t)

	res := upload(t, srv, sc.ID, "reference.png", "PNGBYTES")
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("upload status = %d", res.StatusCode)
	}
	var updated store.Schedule
	json.NewDecoder(res.Body).Decode(&updated)
	if len(updated.Attachments) != 1 || updated.Attachments[0].Name != "reference.png" {
		t.Fatalf("attachments = %+v", updated.Attachments)
	}
	if updated.Attachments[0].Size != int64(len("PNGBYTES")) {
		t.Errorf("size = %d", updated.Attachments[0].Size)
	}

	// It survives a reload: the record is in the schedule's meta column.
	reloaded, err := s.store.ByID(sc.ID)
	if err != nil || len(reloaded.Attachments) != 1 {
		t.Fatalf("reloaded = %+v, %v", reloaded, err)
	}

	// And it lands in the directory a run starts in.
	dir := t.TempDir()
	if n := s.stageAttachments(reloaded, dir); n != 1 {
		t.Fatalf("staged %d, want 1", n)
	}
	b, err := os.ReadFile(filepath.Join(dir, "reference.png"))
	if err != nil || string(b) != "PNGBYTES" {
		t.Errorf("staged file = %q, %v", b, err)
	}
}

// Re-attaching the same name replaces rather than duplicating — the list is
// what the run receives, and two entries could not both be that file.
func TestReAttachingReplaces(t *testing.T) {
	s, srv, sc := attachServer(t)
	upload(t, srv, sc.ID, "ref.png", "first").Body.Close()
	upload(t, srv, sc.ID, "ref.png", "second").Body.Close()

	reloaded, _ := s.store.ByID(sc.ID)
	if len(reloaded.Attachments) != 1 {
		t.Fatalf("attachments = %+v, want one", reloaded.Attachments)
	}
	dir := t.TempDir()
	s.stageAttachments(reloaded, dir)
	b, _ := os.ReadFile(filepath.Join(dir, "ref.png"))
	if string(b) != "second" {
		t.Errorf("staged %q, want the replacement", b)
	}
}

func TestDetachRemovesTheFileAndTheRecord(t *testing.T) {
	s, srv, sc := attachServer(t)
	upload(t, srv, sc.ID, "ref.png", "bytes").Body.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/daily/attachment?schedule="+sc.ID+"&name=ref.png", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("detach status = %d", res.StatusCode)
	}
	reloaded, _ := s.store.ByID(sc.ID)
	if len(reloaded.Attachments) != 0 {
		t.Errorf("attachments = %+v, want none", reloaded.Attachments)
	}
	if _, err := os.Stat(filepath.Join(s.attachRoot(sc.ID), "ref.png")); !os.IsNotExist(err) {
		t.Error("the file outlived its record")
	}
}

// A record whose file has gone is skipped, not fatal: the other attachments
// still reach a run that may not have needed the missing one.
func TestStagingSurvivesAMissingFile(t *testing.T) {
	s, srv, sc := attachServer(t)
	upload(t, srv, sc.ID, "present.txt", "here").Body.Close()
	reloaded, _ := s.store.ByID(sc.ID)
	reloaded.Attachments = append(reloaded.Attachments, store.Attachment{Name: "vanished.txt", Size: 4})

	dir := t.TempDir()
	if n := s.stageAttachments(reloaded, dir); n != 1 {
		t.Errorf("staged %d, want the one that exists", n)
	}
}

func TestAttachingToAnUnknownScheduleIs404(t *testing.T) {
	_, srv, _ := attachServer(t)
	res := upload(t, srv, "no-such-schedule", "ref.png", "x")
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
}

// The staged copy is per occurrence, so an agent rewriting one cannot reach the
// next run — which is the reason the bytes are kept here rather than mounted.
func TestStagingGivesEachRunItsOwnCopy(t *testing.T) {
	s, srv, sc := attachServer(t)
	upload(t, srv, sc.ID, "ref.txt", "original").Body.Close()
	reloaded, _ := s.store.ByID(sc.ID)

	first, second := t.TempDir(), t.TempDir()
	s.stageAttachments(reloaded, first)
	os.WriteFile(filepath.Join(first, "ref.txt"), []byte("clobbered by the agent"), 0o644)
	s.stageAttachments(reloaded, second)

	b, _ := os.ReadFile(filepath.Join(second, "ref.txt"))
	if strings.TrimSpace(string(b)) != "original" {
		t.Errorf("second run got %q, want the original", b)
	}
}
