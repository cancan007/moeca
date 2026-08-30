// Non-text knowledge. A knowledge folder is rarely all Markdown: it holds
// spreadsheets, PDFs, screenshots and screen recordings too. Those cannot be
// chunked as they are — handing an embedding model a PNG's bytes produces a
// vector for noise, and a vector for noise is worse than no vector at all,
// because it still competes for the top-k.
//
// So every supported class is reduced to text in the way that is honest for
// that class, and the Source records WHICH reduction was applied. That second
// part matters as much as the first: a file listed with a chunk count, next to
// an indexed Markdown file, reads as "its contents are searchable". For an
// image today that would be a lie, so the UI is given the means to say
// "path and filename only" instead.
package index

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Media is what a source file IS — the badge the UI shows.
const (
	MediaText     = "text"
	MediaCSV      = "csv"
	MediaSubtitle = "subtitle"
	MediaPDF      = "pdf"
	MediaImage    = "image"
	MediaVideo    = "video"
)

// Content is what was actually INDEXED for that source, which is a different
// question from what the file is. A PDF whose text layer was extracted and a
// screenshot that contributed only its filename are both "indexed", and telling
// them apart is the difference between trusting a search result and being
// misled by one.
const (
	ContentText     = "text"     // the file's own text was chunked
	ContentMetadata = "metadata" // path / size / date only — the bytes were never read
	// ContentCaption means a vision model looked at the file and its
	// description was indexed. Distinct from ContentText on purpose: the words
	// in the index are a model's account of the picture, not anything written
	// in the file, and a UI that showed them as the same would be claiming the
	// image contains text it does not.
	ContentCaption = "caption"
)

// Extension sets per class. textExt (the original whitelist) lives in index.go.
var (
	csvExt = map[string]bool{".csv": true, ".tsv": true}

	// Subtitle/caption tracks are indexed as text of their own: this is how a
	// video's spoken content becomes searchable without ffmpeg ever running
	// inside the indexer. See videoSidecar.
	subtitleExt = map[string]bool{".vtt": true, ".srt": true}

	imageExt = map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
		".bmp": true, ".tif": true, ".tiff": true, ".svg": true, ".heic": true,
		".heif": true, ".avif": true, ".ico": true,
	}

	videoExt = map[string]bool{
		".mp4": true, ".mov": true, ".webm": true, ".mkv": true, ".avi": true,
		".m4v": true, ".mpg": true, ".mpeg": true, ".wmv": true, ".flv": true,
	}
)

// Read caps. Text files were previously read whole with no bound at all; a
// knowledge folder that happens to contain a 400 MB CSV export would have been
// chunked and embedded in full. PDFs get a larger allowance because a scanned
// document is mostly image bytes that never reach the embedder.
const (
	maxTextBytes = 8 << 20  // 8 MiB — text, csv, subtitles
	maxPDFBytes  = 32 << 20 // 32 MiB
	maxCSVRows   = 5000     // rows rendered per spreadsheet
	maxPDFPages  = 500
)

// classify maps a path onto a media class, or "" when the extension is not
// something this indexer knows how to reduce to text.
func classify(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case textExt[ext]:
		return MediaText
	case csvExt[ext]:
		return MediaCSV
	case subtitleExt[ext]:
		return MediaSubtitle
	case ext == ".pdf":
		return MediaPDF
	case imageExt[ext]:
		return MediaImage
	case videoExt[ext]:
		return MediaVideo
	}
	return ""
}

// mediaLabel is the human name used inside a metadata descriptor. The
// descriptor is embedded and can surface in search results, so it is written in
// the UI's language rather than in the code's.
var mediaLabel = map[string]string{
	MediaText:     "テキスト",
	MediaCSV:      "表データ",
	MediaSubtitle: "字幕",
	MediaPDF:      "PDF",
	MediaImage:    "画像",
	MediaVideo:    "動画",
}

// captioner describes a picture for the index, or explains why it could not.
// nil when captioning is off, which is the default.
type captioner func(path string) (string, error)

// reduction is the outcome of turning one file into indexable text.
type reduction struct {
	text    string // "" => nothing to index
	content string // ContentText | ContentMetadata | ContentCaption
	note    string // non-fatal remark for the UI (truncation, no text layer, …)
	err     error  // fatal for this file only; the build continues
}

// reduce turns one local file into text. sidecar is the caption track found
// next to a video, if any (see videoSidecar) — it is named in the video's
// descriptor but not re-chunked here, because the sidecar file is walked and
// indexed as a source in its own right and duplicating it would let one
// transcript occupy several slots of the same top-k.
func reduce(path, rel, media string, info os.FileInfo, sidecar string, describe captioner) reduction {
	switch media {
	case MediaText:
		b, note, err := readCapped(path, maxTextBytes)
		if err != nil {
			return reduction{err: err}
		}
		return reduction{text: string(b), content: ContentText, note: note}

	case MediaCSV:
		b, note, err := readCapped(path, maxTextBytes)
		if err != nil {
			return reduction{err: err}
		}
		comma := ','
		if strings.EqualFold(filepath.Ext(path), ".tsv") {
			comma = '\t'
		}
		text, rowNote, err := csvText(b, comma)
		if err != nil {
			return reduction{err: err}
		}
		return reduction{text: text, content: ContentText, note: joinNotes(note, rowNote)}

	case MediaSubtitle:
		b, note, err := readCapped(path, maxTextBytes)
		if err != nil {
			return reduction{err: err}
		}
		return reduction{text: subtitleText(string(b)), content: ContentText, note: note}

	case MediaPDF:
		if info.Size() > maxPDFBytes {
			return reduction{
				text:    descriptorOf(rel, media, info, ""),
				content: ContentMetadata,
				note:    fmt.Sprintf("サイズ上限(%d MiB)超過のため本文は未抽出", maxPDFBytes>>20),
			}
		}
		text, note, err := pdfText(path)
		if err != nil {
			return reduction{err: err}
		}
		// A scanned PDF parses fine and yields nothing: it is a picture of a
		// page, not a page. Falling back to the descriptor keeps the file
		// findable by name and keeps the reason visible, rather than dropping
		// it silently and leaving the user to wonder where it went.
		if strings.TrimSpace(text) == "" {
			return reduction{
				text:    descriptorOf(rel, media, info, ""),
				content: ContentMetadata,
				note:    "テキスト層なし（スキャンPDFの可能性）。本文は未索引",
			}
		}
		return reduction{text: text, content: ContentText, note: note}

	case MediaImage:
		// Metadata-only unless an operator turned captioning on. It costs a
		// model call per picture, so it is a decision rather than a default —
		// see caption.go.
		descriptor := descriptorOf(rel, media, info, sidecar)
		if describe == nil {
			return reduction{text: descriptor, content: ContentMetadata, note: "画像の内容は未索引（パスとファイル名のみ）"}
		}
		caption, err := describe(path)
		if err != nil {
			// Still in the index, still findable by name — exactly where it was
			// before captioning existed. The reason is carried so the panel can
			// say why this one picture is thinner than its neighbours.
			return reduction{
				text:    descriptor,
				content: ContentMetadata,
				note:    "説明の生成に失敗したため内容は未索引: " + err.Error(),
			}
		}
		// Descriptor AND caption. Dropping the descriptor would trade searching
		// by name for searching by content instead of adding the second.
		return reduction{
			text:    descriptor + "\n\n説明(モデル生成): " + caption,
			content: ContentCaption,
			note:    "内容はモデルが生成した説明として索引済み",
		}

	case MediaVideo:
		// Not captioned, for the reason it was never indexed: describing a
		// video means sampling frames, which means ffmpeg, which would put an
		// untrusted-binary parser inside the one container that holds all of
		// the knowledge. Subtitles are text and need no parser, so that is how
		// a video's content gets in.
		note := "字幕(.vtt/.srt)が無いため本文は未索引"
		if sidecar != "" {
			note = fmt.Sprintf("字幕 %s を別ソースとして索引済み", sidecar)
		}
		return reduction{text: descriptorOf(rel, media, info, sidecar), content: ContentMetadata, note: note}
	}
	return reduction{err: fmt.Errorf("unsupported media class %q", media)}
}

// descriptorOf renders the metadata descriptor that stands in for a file whose
// bytes were not read. It is written to be *retrievable* — the filename is
// repeated on its own line because that is usually what someone searches for —
// and to be *honest*: the last line says outright that the contents are missing,
// so an agent that receives this chunk cannot mistake it for the document.
func descriptorOf(rel, media string, info os.FileInfo, sidecar string) string {
	label := mediaLabel[media]
	if label == "" {
		label = "ファイル"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%sファイル: %s\n", label, rel)
	fmt.Fprintf(&b, "ファイル名: %s\n", filepath.Base(rel))
	fmt.Fprintf(&b, "形式: %s · %s\n", strings.TrimPrefix(strings.ToUpper(filepath.Ext(rel)), "."), humanSize(info.Size()))
	if sidecar != "" {
		fmt.Fprintf(&b, "字幕: %s\n", sidecar)
	}
	b.WriteString("※ このファイルの中身は索引されていません。\n")
	return b.String()
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// readCapped reads at most limit bytes, reporting truncation as a note rather
// than as an error — half a document indexed is better than none, as long as
// the UI can say it is half.
func readCapped(path string, limit int64) ([]byte, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, "", err
	}
	note := ""
	if int64(len(b)) == limit {
		note = fmt.Sprintf("サイズ上限(%d MiB)で切り詰め", limit>>20)
	}
	return b, note, nil
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + " / " + b
}

/* ── CSV ── */

// csvText renders a spreadsheet as one line per row, each cell labelled with
// its column header. A raw CSV chunk embeds badly: `1,2026-04-01,PENDING` has
// no words in it. `状態: PENDING · 期日: 2026-04-01` does, and the header is
// carried into every row so a chunk cut halfway down the file still knows what
// its columns mean.
func csvText(b []byte, comma rune) (string, string, error) {
	r := csv.NewReader(strings.NewReader(string(b)))
	r.Comma = comma
	r.LazyQuotes = true
	r.FieldsPerRecord = -1 // ragged exports are common; do not reject the file

	header, err := r.Read()
	if err == io.EOF {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("csv: %w", err)
	}
	for i := range header {
		header[i] = strings.TrimSpace(header[i])
	}

	var out strings.Builder
	fmt.Fprintf(&out, "列: %s\n", strings.Join(header, ", "))
	rows, note := 0, ""
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// One malformed row should not cost the whole sheet.
			continue
		}
		if rows >= maxCSVRows {
			note = fmt.Sprintf("先頭 %d 行のみ索引", maxCSVRows)
			break
		}
		var cells []string
		for i, v := range rec {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if i < len(header) && header[i] != "" {
				cells = append(cells, header[i]+": "+v)
			} else {
				cells = append(cells, v)
			}
		}
		if len(cells) == 0 {
			continue
		}
		out.WriteString(strings.Join(cells, " · "))
		out.WriteByte('\n')
		rows++
	}
	return out.String(), note, nil
}

/* ── subtitles ── */

var (
	reCueTime = regexp.MustCompile(`-->`)
	reCueTag  = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)
)

// subtitleText strips a WebVTT/SRT track down to what was said: cue numbers,
// timestamps and inline styling carry no meaning for retrieval, and a caption
// that stays on screen across several cues is repeated verbatim in the file, so
// consecutive duplicates are collapsed.
func subtitleText(s string) string {
	var out []string
	var prev string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || reCueTime.MatchString(line) {
			continue
		}
		if strings.HasPrefix(line, "WEBVTT") || strings.HasPrefix(line, "NOTE") || strings.HasPrefix(line, "STYLE") {
			continue
		}
		if _, err := strconv.Atoi(line); err == nil { // SRT cue index
			continue
		}
		line = strings.TrimSpace(reCueTag.ReplaceAllString(line, ""))
		if line == "" || line == prev {
			continue
		}
		out = append(out, line)
		prev = line
	}
	return strings.Join(out, "\n")
}

// videoSidecar finds the caption track that belongs to a video: the same base
// name in the same directory, with or without a language tag
// (`demo.mp4` → `demo.vtt`, `demo.ja.vtt`, `demo.en.srt`). Lookup is against
// the set of files already collected by the walk, so it costs no extra I/O and
// cannot reach outside the mounted root.
func videoSidecar(rel string, all map[string]bool) string {
	dir := filepath.Dir(rel)
	base := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	for cand := range all {
		if filepath.Dir(cand) != dir || !subtitleExt[strings.ToLower(filepath.Ext(cand))] {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(cand), filepath.Ext(cand))
		if name == base || strings.HasPrefix(name, base+".") {
			return cand
		}
	}
	return ""
}

/* ── PDF ── */

// pdfText extracts the text layer page by page. Page markers are kept because
// chunking is line-based, so they survive into the chunk and give a citation
// something to point at.
//
// Extraction runs under recover: this parser walks attacker-shaped input (a PDF
// in a knowledge folder can come from anywhere) and a malformed cross-reference
// table panics rather than erroring. Pure Go makes that a crash, not a
// compromise — but a crash would still take down the indexer mid-build, so each
// page is isolated and a bad one costs a page.
func pdfText(path string) (text string, note string, err error) {
	f, r, err := openPDF(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	pages := r.NumPage()
	truncated := ""
	if pages > maxPDFPages {
		pages = maxPDFPages
		truncated = fmt.Sprintf("先頭 %d ページのみ索引", maxPDFPages)
	}
	var b strings.Builder
	failed := 0
	for n := 1; n <= pages; n++ {
		s, perr := pdfPageText(r, n)
		if perr != nil {
			failed++
			continue
		}
		if strings.TrimSpace(s) == "" {
			continue
		}
		fmt.Fprintf(&b, "--- p.%d ---\n%s\n", n, strings.TrimSpace(s))
	}
	if failed > 0 {
		truncated = joinNotes(truncated, fmt.Sprintf("%d ページの抽出に失敗", failed))
	}
	return b.String(), truncated, nil
}

func openPDF(path string) (f *os.File, r *pdf.Reader, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			if f != nil {
				f.Close()
			}
			f, r, err = nil, nil, fmt.Errorf("pdf: 解析に失敗しました (%v)", rec)
		}
	}()
	f, r, err = pdf.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("pdf: %w", err)
	}
	return f, r, nil
}

func pdfPageText(r *pdf.Reader, n int) (s string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			s, err = "", fmt.Errorf("page %d: %v", n, rec)
		}
	}()
	p := r.Page(n)
	if p.V.IsNull() {
		return "", nil
	}
	return p.GetPlainText(nil)
}
