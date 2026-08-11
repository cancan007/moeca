package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// The agent applies a default reasoning effort of its own, so this is not a
// setting an operator has to reach for — it is on for every run. Sending it to
// a model that does not take it is therefore not an edge case: gpt-4o answers
// `400 Unrecognized request argument supplied: reasoning_effort` and the stage
// dies, naming a parameter nobody set.

func encodedRequest(t *testing.T, model, effort string) map[string]any {
	t.Helper()
	c := &openAIClient{}
	raw, err := json.Marshal(c.encode(Request{
		Model:        model,
		MaxTokens:    100,
		OutputConfig: &OutputConfig{Effort: effort},
		Messages:     []Message{{Role: "user", Content: []Block{TextBlock("hi")}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestChatModelsAreNotSentReasoningEffort(t *testing.T) {
	for _, model := range []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "chatgpt-4o-latest"} {
		if _, present := encodedRequest(t, model, "medium")["reasoning_effort"]; present {
			t.Errorf("%s was sent reasoning_effort; it rejects the whole request", model)
		}
	}
}

func TestReasoningModelsStillGetTheEffort(t *testing.T) {
	for _, model := range []string{"o3", "o3-2025-04-16", "o4-mini", "gpt-5", "gpt-5-mini"} {
		got, _ := encodedRequest(t, model, "low")["reasoning_effort"].(string)
		if got != "low" {
			t.Errorf("%s reasoning_effort = %q, want low", model, got)
		}
	}
}

// An unknown model is treated as not accepting it: losing some depth is
// recoverable, losing the run is not.
func TestUnknownModelsDoNotGetTheEffort(t *testing.T) {
	if _, present := encodedRequest(t, "some-new-model", "high")["reasoning_effort"]; present {
		t.Error("an unrecognised model was sent reasoning_effort")
	}
}

// The clamp is unchanged for the models that do take it.
func TestEffortClampsToWhatOpenAIAccepts(t *testing.T) {
	got, _ := encodedRequest(t, "o3", "max")["reasoning_effort"].(string)
	if got != "high" {
		t.Errorf("max mapped to %q, want high", got)
	}
	if _, present := encodedRequest(t, "o3", "")["reasoning_effort"]; present {
		t.Error("an unset effort still sent the field")
	}
}

func TestAcceptsEffortIsNotFooledByAPrefix(t *testing.T) {
	// "o1" must not match "o1x" or an unrelated id that merely starts with it.
	for _, m := range []string{"o1x", "o3pro", "gpt-5x"} {
		if acceptsEffort(m) {
			t.Errorf("acceptsEffort(%q) = true", m)
		}
	}
	if !strings.HasPrefix("o3-mini", "o3-") {
		t.Fatal("test premise")
	}
}
