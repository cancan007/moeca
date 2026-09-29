package llm

import "testing"

// The entrypoint and the encoder must agree on which dialects can search.
//
// They did not, once. The Responses encoder learned to translate the web_search
// grant while main.go still registered it for Anthropic alone, so on an OpenAI
// model the tool was never advertised — and an agent that is not offered a tool
// does not report a missing tool, it just uses a different one. The symptom was
// an agent searching the knowledge base over and over for something that was
// only ever going to be on the web.
func TestDialectSearches(t *testing.T) {
	for _, kind := range []string{KindAnthropic, KindOpenAIResponses} {
		if !DialectSearches(kind) {
			t.Errorf("DialectSearches(%q) = false, want true", kind)
		}
	}
	// An unset dialect is Anthropic — the same default the rest of the runtime
	// applies, so the grant must survive it.
	if !DialectSearches("") {
		t.Error(`DialectSearches("") = false; an unset dialect means Anthropic`)
	}
	if !DialectSearches("  OpenAI-Responses  ") {
		t.Error("DialectSearches is case- or space-sensitive; the value comes from config")
	}

	// These two drop server tools when encoding, so registering the grant would
	// advertise a tool nothing executes.
	for _, kind := range []string{KindOpenAI, KindGemini, "something-new"} {
		if DialectSearches(kind) {
			t.Errorf("DialectSearches(%q) = true, want false", kind)
		}
	}
}
