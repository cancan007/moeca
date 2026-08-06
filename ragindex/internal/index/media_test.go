package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := map[string]string{
		"docs/a.md":       MediaText,
		"docs/a.MD":       MediaText,
		"export.csv":      MediaCSV,
		"export.tsv":      MediaCSV,
		"talk.vtt":        MediaSubtitle,
		"talk.SRT":        MediaSubtitle,
		"spec.pdf":        MediaPDF,
		"shot.png":        MediaImage,
		"diagram.svg":     MediaImage,
		"demo.mp4":        MediaVideo,
		"archive.zip":     "",
		"binary":          "",
		"Makefile":        "",
		"lib.so":          "",
		"notes.md.backup": "",
	}
	for path, want := range cases {
		if got := classify(path); got != want {
			t.Errorf("classify(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCSVTextLabelsCellsWithHeaders(t *testing.T) {
	in := []byte("id,status,due\n1,PENDING,2026-04-01\n2,,2026-05-02\n")
	got, note, err := csvText(in, ',')
	if err != nil {
		t.Fatalf("csvText: %v", err)
	}
	if note != "" {
		t.Errorf("unexpected note %q", note)
	}
	if !strings.Contains(got, "列: id, status, due") {
		t.Errorf("header line missing:\n%s", got)
	}
	if !strings.Contains(got, "id: 1 · status: PENDING · due: 2026-04-01") {
		t.Errorf("row not labelled with headers:\n%s", got)
	}
	// An empty cell contributes no `status: ` noise.
	if strings.Contains(got, "status: \n") || strings.Contains(got, "status:  ") {
		t.Errorf("empty cell should be dropped:\n%s", got)
	}
}

func TestCSVTextRaggedRowsDoNotFailTheFile(t *testing.T) {
	in := []byte("a,b\n1,2\n3\n4,5,6\n")
	got, _, err := csvText(in, ',')
	if err != nil {
		t.Fatalf("ragged csv should not error: %v", err)
	}
	for _, want := range []string{"a: 1 · b: 2", "a: 3", "a: 4 · b: 5 · 6"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestCSVTextTruncatesAtRowCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("n\n")
	for i := 0; i < maxCSVRows+50; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	got, note, err := csvText([]byte(b.String()), ',')
	if err != nil {
		t.Fatalf("csvText: %v", err)
	}
	if note == "" {
		t.Errorf("expected a truncation note")
	}
	if lines := strings.Count(got, "\n"); lines > maxCSVRows+1 {
		t.Errorf("rendered %d lines, want <= %d", lines, maxCSVRows+1)
	}
}

func TestSubtitleTextStripsCuesAndDuplicates(t *testing.T) {
	vtt := "WEBVTT\n\nNOTE recorded 2026-04-01\n\n1\n00:00:01.000 --> 00:00:03.000\n<v Speaker>索引の再構築について\n\n2\n00:00:03.000 --> 00:00:06.000\n索引の再構築について\n\n3\n00:00:06.000 --> 00:00:09.000\nバッチは 500 件ずつ\n"
	got := subtitleText(vtt)
	want := "索引の再構築について\nバッチは 500 件ずつ"
	if got != want {
		t.Errorf("subtitleText =\n%q\nwant\n%q", got, want)
	}
}

func TestVideoSidecarMatchesLanguageTaggedTracks(t *testing.T) {
	all := map[string]bool{
		"media/demo.mp4":     true,
		"media/demo.ja.vtt":  true,
		"other/demo.srt":     true, // different directory — must not match
		"media/unrelated.md": true,
	}
	if got := videoSidecar("media/demo.mp4", all); got != "media/demo.ja.vtt" {
		t.Errorf("videoSidecar = %q, want media/demo.ja.vtt", got)
	}
	if got := videoSidecar("media/none.mp4", all); got != "" {
		t.Errorf("videoSidecar for a video with no track = %q, want \"\"", got)
	}
}

// An image contributes a descriptor, not contents — and the descriptor has to
// say so, because this text is what an agent receives.
func TestReduceImageIsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logo.png")
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\nnot really a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	red := reduce(path, "assets/logo.png", MediaImage, info, "")
	if red.err != nil {
		t.Fatalf("reduce: %v", red.err)
	}
	if red.content != ContentMetadata {
		t.Errorf("content = %q, want %q", red.content, ContentMetadata)
	}
	if !strings.Contains(red.text, "assets/logo.png") || !strings.Contains(red.text, "logo.png") {
		t.Errorf("descriptor should carry path and filename:\n%s", red.text)
	}
	if !strings.Contains(red.text, "索引されていません") {
		t.Errorf("descriptor must state that contents are missing:\n%s", red.text)
	}
	if !strings.Contains(red.text, "PNG") {
		t.Errorf("descriptor should name the format:\n%s", red.text)
	}
}

func TestReduceVideoNamesItsSidecar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.mp4")
	if err := os.WriteFile(path, []byte("\x00\x00\x00\x18ftypmp42"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)

	with := reduce(path, "media/demo.mp4", MediaVideo, info, "media/demo.ja.vtt")
	if with.content != ContentMetadata {
		t.Errorf("content = %q, want %q", with.content, ContentMetadata)
	}
	if !strings.Contains(with.text, "media/demo.ja.vtt") {
		t.Errorf("descriptor should name the caption track:\n%s", with.text)
	}
	if !strings.Contains(with.note, "字幕") {
		t.Errorf("note should mention the sidecar, got %q", with.note)
	}

	without := reduce(path, "media/demo.mp4", MediaVideo, info, "")
	if !strings.Contains(without.note, "字幕") {
		t.Errorf("a video with no track should say so, got %q", without.note)
	}
}

// A PDF that parses but holds no text layer (a scan) must not vanish: it falls
// back to the descriptor with the reason attached.
func TestReducePDFWithoutTextLayerFallsBackToMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan.pdf")
	if err := os.WriteFile(path, minimalPDF(""), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	red := reduce(path, "docs/scan.pdf", MediaPDF, info, "")
	if red.err != nil {
		t.Fatalf("reduce: %v", red.err)
	}
	if red.content != ContentMetadata {
		t.Errorf("content = %q, want %q", red.content, ContentMetadata)
	}
	if !strings.Contains(red.note, "テキスト層") {
		t.Errorf("note should explain the empty extraction, got %q", red.note)
	}
}

func TestReducePDFExtractsTextLayer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.pdf")
	if err := os.WriteFile(path, minimalPDF("Reindex in batches of 500"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	red := reduce(path, "docs/spec.pdf", MediaPDF, info, "")
	if red.err != nil {
		t.Fatalf("reduce: %v", red.err)
	}
	if red.content != ContentText {
		t.Fatalf("content = %q, want %q (text %q)", red.content, ContentText, red.text)
	}
	if !strings.Contains(red.text, "Reindex in batches of 500") {
		t.Errorf("extracted text missing the page content:\n%s", red.text)
	}
	if !strings.Contains(red.text, "--- p.1 ---") {
		t.Errorf("page marker missing:\n%s", red.text)
	}
}

// A corrupt PDF is recorded against its own source and does not take the build
// down with it — the parser panics on malformed structure rather than erroring.
func TestReducePDFCorruptIsRecordedNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\nnot a pdf at all\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	red := reduce(path, "docs/broken.pdf", MediaPDF, info, "")
	if red.err == nil {
		t.Fatalf("expected a per-file error, got text %q", red.text)
	}
}

func TestReadCappedReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	b, note, err := readCapped(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 10 {
		t.Errorf("read %d bytes, want 10", len(b))
	}
	if note == "" {
		t.Errorf("expected a truncation note")
	}
}

// A folder of mixed media indexes in one pass: text and spreadsheets and
// caption tracks contribute chunks of their own contents, images and videos
// contribute a descriptor, and the Source rows say which is which.
func TestBuildIndexesMixedMedia(t *testing.T) {
	gw := mockGateway(t)
	defer gw.Close()

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "media"), 0o755)
	write := func(rel string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.md", []byte("the alpha protocol"))
	write("rows.csv", []byte("id,note\n1,alpha row\n"))
	write("spec.pdf", minimalPDF("alpha specification"))
	write("media/demo.mp4", []byte("\x00\x00\x00\x18ftypmp42"))
	write("media/demo.ja.vtt", []byte("WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nalpha spoken\n"))
	write("media/shot.png", []byte("\x89PNG\r\n\x1a\n"))
	write("archive.zip", []byte("PK\x03\x04")) // still unsupported

	idx := New(Config{Root: root, Gateway: gw.URL, Session: "sess", EmbedPrefix: "/openai", EmbedModel: "m"})
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}

	byPath := map[string]Source{}
	for _, s := range idx.Status().Sources {
		byPath[filepath.ToSlash(s.Path)] = s
	}
	if _, ok := byPath["archive.zip"]; ok {
		t.Errorf("unsupported extension should not become a source")
	}
	for _, want := range []struct{ path, media, content string }{
		{"notes.md", MediaText, ContentText},
		{"rows.csv", MediaCSV, ContentText},
		{"spec.pdf", MediaPDF, ContentText},
		{"media/demo.ja.vtt", MediaSubtitle, ContentText},
		{"media/demo.mp4", MediaVideo, ContentMetadata},
		{"media/shot.png", MediaImage, ContentMetadata},
	} {
		got, ok := byPath[want.path]
		if !ok {
			t.Errorf("%s missing from sources", want.path)
			continue
		}
		if got.Media != want.media || got.Content != want.content {
			t.Errorf("%s: media/content = %q/%q, want %q/%q", want.path, got.Media, got.Content, want.media, want.content)
		}
		if got.Chunks == 0 {
			t.Errorf("%s: 0 chunks — even a descriptor is one chunk", want.path)
		}
		if got.Error != "" {
			t.Errorf("%s: unexpected error %q", want.path, got.Error)
		}
	}
	// The transcript is indexed once, as the sidecar's own source — not a
	// second time under the video, which would let one transcript take two
	// slots of the same top-k.
	if n := byPath["media/demo.mp4"].Chunks; n != 1 {
		t.Errorf("video should contribute exactly its descriptor, got %d chunks", n)
	}
	if !strings.Contains(byPath["media/demo.mp4"].Note, "demo.ja.vtt") {
		t.Errorf("video note should point at its track, got %q", byPath["media/demo.mp4"].Note)
	}
}

// minimalPDF builds a one-page PDF with an uncompressed content stream, so the
// text-layer path is exercised against a real parse rather than a stub. Passing
// an empty string yields a page with no text — a stand-in for a scan.
func minimalPDF(text string) []byte {
	content := ""
	if text != "" {
		content = fmt.Sprintf("BT /F1 24 Tf 72 700 Td (%s) Tj ET\n", text)
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return []byte(b.String())
}
