package gateway

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file reconciles the token budget against REAL provider usage instead of a
// byte estimate. Provider responses report token counts under dialect-specific
// keys; the gateway scans the response tail for them. Because usage sits at the
// end of the JSON (or in the terminal SSE frame), the last match wins.
//
//	Anthropic: usage.input_tokens / usage.output_tokens
//	OpenAI:    usage.prompt_tokens / usage.completion_tokens
//	Gemini:    usageMetadata.promptTokenCount / usageMetadata.candidatesTokenCount

// usageKeys names a dialect's usage fields. total is the reported sum where the
// provider publishes one; it is what lets a response with no output side be
// charged exactly rather than guessed at.
type usageKeys struct{ in, out, total string }

// dialectKeys maps a routed service name to its usage field names. Names line up
// with the llm.Kind* constants and the default gateway config service keys.
var dialectKeys = map[string]usageKeys{
	"anthropic": {"input_tokens", "output_tokens", ""},
	"openai":    {"prompt_tokens", "completion_tokens", "total_tokens"},
	"gemini":    {"promptTokenCount", "candidatesTokenCount", "totalTokenCount"},
}

// allDialects is the fallback probe order for custom-named providers.
var allDialects = []usageKeys{
	{"input_tokens", "output_tokens", ""},
	{"prompt_tokens", "completion_tokens", "total_tokens"},
	{"promptTokenCount", "candidatesTokenCount", "totalTokenCount"},
}

var intFieldRe = map[string]*regexp.Regexp{}

func fieldRe(key string) *regexp.Regexp {
	if re, ok := intFieldRe[key]; ok {
		return re
	}
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*:\s*(\d+)`)
	intFieldRe[key] = re
	return re
}

// lastInt returns the last integer value for key found in body (last, so the
// terminal/cumulative value wins for streamed responses).
func lastInt(body []byte, key string) (int, bool) {
	m := fieldRe(key).FindAllSubmatch(body, -1)
	if len(m) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(string(m[len(m)-1][1]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// extractUsage parses input/output token counts from a model response tail,
// selecting the parser by the routed service name and falling back to probing
// every known dialect for custom-named providers.
//
// streamed says the response was an event stream. It matters because the tail
// is the LAST few kilobytes: in a stream the two counts arrive in different
// frames, so a missing one may simply have scrolled out of view, and charging
// the half that is visible would undercount. In a single JSON body there is no
// such doubt — the usage object is whole, so a field that is not there is a
// field the provider does not have.
//
// That distinction is the whole fix. Requiring both counts unconditionally
// meant an embeddings response — which reports a prompt count and no completion
// count, because nothing was completed — fell back to the byte estimate. That
// estimate then read a megabyte of returned vectors as if it were prose the
// model had produced, and charged half a million tokens for ten thousand.
func extractUsage(tail []byte, name string, streamed bool) (in, out int, ok bool) {
	if k, known := dialectKeys[name]; known {
		return usageFor(tail, k, streamed)
	}
	for _, k := range allDialects {
		if i, o, found := usageFor(tail, k, streamed); found {
			return i, o, true
		}
	}
	return 0, 0, false
}

func usageFor(tail []byte, k usageKeys, streamed bool) (int, int, bool) {
	in, okIn := lastInt(tail, k.in)
	out, okOut := lastInt(tail, k.out)
	if okIn && okOut {
		return in, out, true
	}
	// A reported total settles it without guessing: whatever is not input is
	// output, and for a response with no output side that difference is zero.
	if k.total != "" {
		if total, okTotal := lastInt(tail, k.total); okTotal {
			if okIn {
				if rest := total - in; rest > 0 {
					return in, rest, true
				}
				return in, 0, true
			}
			return total, 0, true
		}
	}
	if streamed {
		return 0, 0, false // the missing half may just be out of the window
	}
	if okIn || okOut {
		return in, out, true
	}
	return 0, 0, false
}

// isTextual reports whether a response body is the kind of thing a model reads.
//
// The byte estimate behind the token budget assumes prose: roughly four bytes
// per token. That holds for JSON and SSE and fails completely for a PNG or an
// mp4, where the bytes are an artifact the model never sees. Charging those as
// tokens is not a rounding error — it is a category mistake, and it was enough
// to exhaust a session's whole budget on three generated files.
func isTextual(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch {
	case ct == "":
		return true // unknown: assume text, which is the conservative charge
	case strings.HasPrefix(ct, "text/"):
		return true
	case strings.Contains(ct, "json"), strings.Contains(ct, "event-stream"),
		strings.Contains(ct, "xml"), strings.Contains(ct, "yaml"):
		return true
	default:
		return false
	}
}

// isStream reports whether a response arrived as an event stream, where the
// usage fields are spread across frames and the tail may hold only some of them.
func isStream(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "event-stream")
}

// byteSize renders a byte count the way an operator reading an error would
// rather see it than as a bare integer.
func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
