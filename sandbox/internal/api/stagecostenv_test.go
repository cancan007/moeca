package api

import "testing"

// The agent runtime has summarized its own history since it had a tool loop, but
// nothing ever set the two variables that configure it — so every run used the
// built-in 120k/6 no matter what the agent was declared with. These assertions
// exist so that stays fixed.
func TestStageCostEnvCarriesCompactionSettings(t *testing.T) {
	env := map[string]string{}
	applyStageCostEnv(env, Stage{Effort: "low", MaxTokens: 8000, MaxContext: 64000, KeepRecent: 8})

	want := map[string]string{
		"ORCHESTRA_EFFORT":             "low",
		"ORCHESTRA_MAX_TOKENS":         "8000",
		"ORCHESTRA_MAX_CONTEXT_TOKENS": "64000",
		"ORCHESTRA_KEEP_RECENT":        "8",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
}

// An omitted control must stay omitted: the agent's own default is the answer,
// and writing a zero would override it with "never".
func TestStageCostEnvLeavesUnsetControlsAlone(t *testing.T) {
	env := map[string]string{}
	applyStageCostEnv(env, Stage{})
	if len(env) != 0 {
		t.Errorf("env = %v, want empty so the agent's defaults apply", env)
	}
}

// Turning compaction off has to be expressible. Zero cannot say it — that is
// also what an absent field decodes to — so a negative value is the request, and
// it reaches the agent as the 0 that means "never compact".
func TestStageCostEnvCanTurnCompactionOff(t *testing.T) {
	env := map[string]string{}
	applyStageCostEnv(env, Stage{MaxContext: -1})
	if env["ORCHESTRA_MAX_CONTEXT_TOKENS"] != "0" {
		t.Errorf("ORCHESTRA_MAX_CONTEXT_TOKENS = %q, want \"0\" (compaction disabled)",
			env["ORCHESTRA_MAX_CONTEXT_TOKENS"])
	}
}
