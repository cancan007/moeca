package gateway

import (
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

type usageKeys struct{ in, out string }

// dialectKeys maps a routed service name to its usage field names. Names line up
// with the llm.Kind* constants and the default gateway config service keys.
var dialectKeys = map[string]usageKeys{
	"anthropic": {"input_tokens", "output_tokens"},
	"openai":    {"prompt_tokens", "completion_tokens"},
	"gemini":    {"promptTokenCount", "candidatesTokenCount"},
}

// allDialects is the fallback probe order for custom-named providers.
var allDialects = []usageKeys{
	{"input_tokens", "output_tokens"},
	{"prompt_tokens", "completion_tokens"},
	{"promptTokenCount", "candidatesTokenCount"},
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
// every known dialect for custom-named providers. ok is true only when BOTH
// counts are present, so a partial parse falls back to the byte estimate rather
// than undercounting.
func extractUsage(tail []byte, name string) (in, out int, ok bool) {
	if k, known := dialectKeys[name]; known {
		return usageFor(tail, k)
	}
	for _, k := range allDialects {
		if i, o, found := usageFor(tail, k); found {
			return i, o, true
		}
	}
	return 0, 0, false
}

func usageFor(tail []byte, k usageKeys) (int, int, bool) {
	in, okIn := lastInt(tail, k.in)
	out, okOut := lastInt(tail, k.out)
	if okIn && okOut {
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
