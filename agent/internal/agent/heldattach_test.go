package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"orchestra/agent/internal/llm"
	"orchestra/agent/internal/tools"
)

// A turn the provider is still working on may be answered with tool results and
// nothing else. A run died sending three images alongside them, so the images
// wait for a turn where that is allowed — and then they must actually arrive.

func rawBlock(t *testing.T, v any) llm.Block {
	t.Helper()
	b, _ := json.Marshal(v)
	var blk llm.Block
	if err := json.Unmarshal(b, &blk); err != nil {
		t.Fatal(err)
	}
	return blk
}

// viewResp asks to view a.png, optionally with a provider-side call still
// running in the same turn.
func viewResp(t *testing.T, pending bool) *llm.Response {
	content := []llm.Block{}
	if pending {
		content = append(content, rawBlock(t, map[string]any{
			"type": "server_tool_use", "id": "srvtoolu_1", "name": "bash_code_execution", "input": map[string]any{},
		}))
	}
	content = append(content, llm.Block{
		Type: llm.BlockToolUse, ID: "toolu_1", Name: "view_image", Input: json.RawMessage(`{"path":"a.png"}`),
	})
	return &llm.Response{Role: "assistant", StopReason: "tool_use", Content: content, Usage: llm.Usage{InputTokens: 10}}
}

func imagesInMessages(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == llm.BlockImage {
				n++
			}
		}
	}
	return n
}

func runWithImage(t *testing.T, fn func(i int, req llm.Request) *llm.Response) *fakeProvider {
	t.Helper()
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "a.png"), []byte("\x89PNG small"), 0o644)
	prov := &fakeProvider{fn: fn}
	r := NewRunner(Config{Task: "TASK", Provider: prov, Tools: tools.New(work), MaxIter: 6})
	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return prov
}

func TestImagesWaitWhileAProviderToolIsRunning(t *testing.T) {
	prov := runWithImage(t, func(i int, _ llm.Request) *llm.Response {
		if i == 0 {
			return viewResp(t, true) // a server call is still in flight
		}
		return textResp("done", 10, "end_turn")
	})

	// The turn answering the pending call must carry tool results and nothing
	// else — that is exactly what the provider rejected.
	if n := imagesInMessages(prov.reqs[1].Messages); n != 0 {
		t.Errorf("%d images rode back with the results of a turn still running", n)
	}
}

func TestHeldImagesArriveOnALaterTurn(t *testing.T) {
	prov := runWithImage(t, func(i int, _ llm.Request) *llm.Response {
		switch i {
		case 0:
			return viewResp(t, true) // held here
		case 1:
			return textResp("continuing", 10, "tool_use") // nothing pending now
		default:
			return textResp("done", 10, "end_turn")
		}
	})

	// Held is not dropped: by the last request the picture must have been sent.
	last := prov.reqs[len(prov.reqs)-1]
	if imagesInMessages(last.Messages) == 0 {
		t.Error("the held image never reached the model")
	}
}

// The ordinary case is unchanged: with nothing running, images ride along with
// the results as they always did.
func TestImagesRideAlongWhenNothingIsPending(t *testing.T) {
	prov := runWithImage(t, func(i int, _ llm.Request) *llm.Response {
		if i == 0 {
			return viewResp(t, false)
		}
		return textResp("done", 10, "end_turn")
	})

	if n := imagesInMessages(prov.reqs[1].Messages); n != 1 {
		t.Errorf("%d images sent; the ordinary path must not be held up", n)
	}
}
