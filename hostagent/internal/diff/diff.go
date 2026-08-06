// Package diff parses git's unified diff output into the structured hunk/line
// model the Orchestra frontend renders (see the review DiffPane). Producing this
// shape server-side means the UI shows real diffs with no client-side parsing.
package diff

import (
	"bufio"
	"strconv"
	"strings"
)

// LineType mirrors the frontend's diff line kinds.
type LineType string

const (
	Meta    LineType = "meta"    // file header (--- / +++ / diff --git)
	Hunk    LineType = "hunk"    // @@ -a,b +c,d @@
	Add     LineType = "add"
	Del     LineType = "del"
	Context LineType = "context"
)

// Line is one rendered diff row.
type Line struct {
	Type    LineType `json:"type"`
	Content string   `json:"content"`
	OldNo   *int     `json:"oldNo,omitempty"`
	NewNo   *int     `json:"newNo,omitempty"`
}

// File is a single file's diff.
type File struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Lines     []Line `json:"lines"`
}

func ptr(n int) *int { return &n }

// Parse converts a (possibly multi-file) unified diff into structured files.
func Parse(unified string) []File {
	files := []File{}
	var cur *File
	oldNo, newNo := 0, 0

	sc := bufio.NewScanner(strings.NewReader(unified))
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			// Seed Path/Lines from the header: binary files and rename- or
			// mode-only changes have no "+++" and no hunks, and the frontend
			// maps over .lines and keys tabs by .path in render.
			files = append(files, File{Path: headerPath(line), Lines: []Line{}})
			cur = &files[len(files)-1]
		case strings.HasPrefix(line, "+++ "):
			// "/dev/null" is a deletion — keep the header path in that case.
			if p := trimDiffPath(strings.TrimPrefix(line, "+++ ")); cur != nil && p != "/dev/null" {
				cur.Path = p
			}
		case strings.HasPrefix(line, "--- "):
			// path taken from +++; ignore
		case strings.HasPrefix(line, "@@"):
			if cur == nil {
				continue
			}
			oldNo, newNo = parseHunkStarts(line)
			cur.Lines = append(cur.Lines, Line{Type: Hunk, Content: line})
		case cur == nil:
			// preamble before first file; skip
		case strings.HasPrefix(line, "+"):
			cur.Additions++
			cur.Lines = append(cur.Lines, Line{Type: Add, Content: line[1:], NewNo: ptr(newNo)})
			newNo++
		case strings.HasPrefix(line, "-"):
			cur.Deletions++
			cur.Lines = append(cur.Lines, Line{Type: Del, Content: line[1:], OldNo: ptr(oldNo)})
			oldNo++
		case strings.HasPrefix(line, " "):
			cur.Lines = append(cur.Lines, Line{Type: Context, Content: line[1:], OldNo: ptr(oldNo), NewNo: ptr(newNo)})
			oldNo++
			newNo++
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" — ignore
		default:
			// index / mode / similarity headers — not rendered as diff rows
		}
	}
	return files
}

// headerPath pulls the b-side path out of a `diff --git a/x b/x` line, so files
// without a "+++" row (binary, rename-only, mode-only) still get a name.
func headerPath(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return strings.TrimSpace(rest[i+3:])
	}
	return trimDiffPath(rest)
}

// trimDiffPath strips the "b/" prefix and any trailing tab metadata.
func trimDiffPath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.IndexByte(p, '\t'); i >= 0 {
		p = p[:i]
	}
	return strings.TrimPrefix(p, "b/")
}

// parseHunkStarts reads "@@ -oldStart,oldLen +newStart,newLen @@" starts.
func parseHunkStarts(header string) (oldStart, newStart int) {
	parts := strings.Fields(header)
	for _, p := range parts {
		if strings.HasPrefix(p, "-") {
			oldStart = firstInt(p[1:])
		} else if strings.HasPrefix(p, "+") {
			newStart = firstInt(p[1:])
		}
	}
	return
}

func firstInt(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, _ := strconv.Atoi(s)
	return n
}
