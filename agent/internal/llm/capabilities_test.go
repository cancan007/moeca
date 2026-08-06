package llm

import (
	"encoding/json"
	"testing"
)

// The model that took down a real run: a tester stage pinned to Haiku 4.5 got
// "adaptive thinking is not supported on this model" and failed the whole DAG.
func TestNormalizeDowngradesLegacyModels(t *testing.T) {
	req := Request{
		Model:        "claude-haiku-4-5-20251001",
		MaxTokens:    16000,
		Thinking:     &Thinking{Type: "adaptive"},
		OutputConfig: &OutputConfig{Effort: "medium"},
	}
	got := normalize(req)

	if got.Thinking == nil {
		t.Fatal("thinking dropped entirely; want the legacy enabled form")
	}
	if got.Thinking.Type != "enabled" {
		t.Errorf("thinking.type = %q, want enabled", got.Thinking.Type)
	}
	if got.Thinking.BudgetTokens < minThinkingBudget {
		t.Errorf("budget_tokens = %d, want >= %d", got.Thinking.BudgetTokens, minThinkingBudget)
	}
	if got.Thinking.BudgetTokens >= got.MaxTokens {
		t.Errorf("budget_tokens = %d must be < max_tokens %d", got.Thinking.BudgetTokens, got.MaxTokens)
	}
	// effort is rejected on this tier too — a second 400 the original code would
	// have hit as soon as thinking was fixed.
	if got.OutputConfig != nil {
		t.Errorf("output_config = %+v, want nil on a legacy model", got.OutputConfig)
	}
}

// Frontier models must keep adaptive thinking and effort untouched.
func TestNormalizeLeavesModernModelsAlone(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-8", "claude-opus-5", "claude-sonnet-5",
		"claude-sonnet-4-6", "claude-opus-4-6", "claude-fable-5",
	} {
		req := Request{
			Model:        model,
			MaxTokens:    16000,
			Thinking:     &Thinking{Type: "adaptive"},
			OutputConfig: &OutputConfig{Effort: "medium"},
		}
		got := normalize(req)
		if got.Thinking == nil || got.Thinking.Type != "adaptive" {
			t.Errorf("%s: thinking = %+v, want adaptive", model, got.Thinking)
			continue
		}
		if got.Thinking.BudgetTokens != 0 {
			t.Errorf("%s: budget_tokens must stay unset (the API 400s on it)", model)
		}
		if got.OutputConfig == nil || got.OutputConfig.Effort != "medium" {
			t.Errorf("%s: effort was dropped", model)
		}
	}
}

// A legacy model with too small a max_tokens can't carry a valid budget
// (min 1024, and it must be < max_tokens) — run without thinking instead of
// sending an invalid budget.
func TestNormalizeDropsThinkingWhenBudgetWontFit(t *testing.T) {
	got := normalize(Request{
		Model:     "claude-haiku-4-5",
		MaxTokens: 1500, // half is 750, below the 1024 floor
		Thinking:  &Thinking{Type: "adaptive"},
	})
	if got.Thinking != nil {
		t.Errorf("thinking = %+v, want nil when no valid budget fits", got.Thinking)
	}
}

// budget_tokens must never be serialized for the adaptive form — the API
// rejects it with a 400 on every model that supports adaptive.
func TestThinkingOmitsBudgetWhenZero(t *testing.T) {
	b, err := json.Marshal(Thinking{Type: "adaptive"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"type":"adaptive"}`; got != want {
		t.Errorf("marshal = %s, want %s", got, want)
	}
}
