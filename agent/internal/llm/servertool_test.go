package llm

import (
	"encoding/json"
	"testing"
)

// The provider runs some tools itself, and such a call can straddle a turn: the
// `server_tool_use` arrives in one response and its result at the top of the
// next. A turn in that state may be answered with tool results and nothing
// else — a run died sending three images alongside them.

func raw(t *testing.T, v any) Block {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var blk Block
	if err := json.Unmarshal(b, &blk); err != nil {
		t.Fatal(err)
	}
	return blk
}

func serverCall(t *testing.T, id string) Block {
	return raw(t, map[string]any{"type": "server_tool_use", "id": id, "name": "bash_code_execution", "input": map[string]any{}})
}

func serverResult(t *testing.T, id string) Block {
	return raw(t, map[string]any{"type": "bash_code_execution_tool_result", "tool_use_id": id, "content": map[string]any{"stdout": "ok"}})
}

func TestACallStillRunningIsPending(t *testing.T) {
	// The exact shape that killed a run: a trailing server call, then the
	// client tools the model asked for in the same turn.
	content := []Block{
		TextBlock("checking"),
		serverCall(t, "srvtoolu_1"),
		{Type: BlockToolUse, ID: "toolu_1", Name: "view_image"},
	}
	if !PendingServerToolUse(content) {
		t.Error("a server call with no result was not reported as pending")
	}
}

func TestACallWithItsResultIsNotPending(t *testing.T) {
	content := []Block{
		serverCall(t, "srvtoolu_1"),
		serverResult(t, "srvtoolu_1"),
		{Type: BlockToolUse, ID: "toolu_1", Name: "view_image"},
	}
	if PendingServerToolUse(content) {
		t.Error("a completed server call was reported as pending")
	}
}

// A result carried over from the PREVIOUS turn does not answer this turn's
// call — the ids have to match, not merely both be present.
func TestAResultForAnotherCallDoesNotSettleThisOne(t *testing.T) {
	content := []Block{
		serverResult(t, "srvtoolu_earlier"),
		serverCall(t, "srvtoolu_now"),
	}
	if !PendingServerToolUse(content) {
		t.Error("an unrelated result was taken as this call's answer")
	}
}

// The ordinary turn: no server tools at all, nothing held back.
func TestATurnWithNoServerToolsIsNotPending(t *testing.T) {
	content := []Block{
		TextBlock("here you go"),
		{Type: BlockToolUse, ID: "toolu_1", Name: "read_file"},
	}
	if PendingServerToolUse(content) {
		t.Error("a turn with no server tools was reported as pending")
	}
}

// Several calls in one turn: one unanswered is enough.
func TestOneUnansweredCallAmongSeveralIsPending(t *testing.T) {
	content := []Block{
		serverCall(t, "a"), serverResult(t, "a"),
		serverCall(t, "b"), serverResult(t, "b"),
		serverCall(t, "c"),
	}
	if !PendingServerToolUse(content) {
		t.Error("the unanswered third call was missed")
	}
}

func TestAMalformedBlockIsNotTreatedAsACall(t *testing.T) {
	content := []Block{{Type: "server_tool_use", Raw: []byte("{not json")}}
	if PendingServerToolUse(content) {
		t.Error("an undecodable block was counted as a pending call")
	}
}
