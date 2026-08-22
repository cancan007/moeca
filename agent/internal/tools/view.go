// Looking at what a run produced.
//
// An agent could always be TOLD that a file exists — the handoff manifest names
// it, list_files shows it, read_file reports its size and refuses the bytes.
// None of that lets it see the picture. So a run could generate an image, hand
// it to an integrator whose whole job was to check it, and the integrator would
// sign off having looked at a filename.
//
// view_image closes that. The bytes go to the model as an image, in whichever
// shape its dialect wants (see llm.BlockImage and the per-dialect encoders),
// and the model can then say whether what was produced is what was asked for.
//
// Deliberately a separate tool from read_file rather than a smarter read_file:
// an image costs tokens on every subsequent turn of the conversation, so
// looking at one should be something the model decides to do, not something
// that happens because it asked what was in a file.
package tools

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"orchestra/agent/internal/llm"
)

// maxViewBytes caps what may be attached. A 1024x1024 PNG is around 2 MB, so
// this admits generated artifacts while refusing something that would dominate
// the context for the rest of the run.
const maxViewBytes = 6 << 20

// maxTurnAttachBytes caps what ONE turn may attach, across every view call in
// it. The per-file limit above is not enough on its own: three separately-legal
// images went out as a single 10.9 MB request, past the gateway's 8 MiB body
// cap, and the run died holding work it had already paid for.
//
// A model that wants to compare several pictures can still do it — one turn
// each. Deferring is recoverable; a request that cannot be sent is not.
const maxTurnAttachBytes = 4 << 20

// viewableTypes maps an extension to the media type sent to the model. Only
// formats every dialect accepts are listed: an unsupported one is better
// refused here, with the reason, than turned into a provider error.
var viewableTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
}

// attachment is an image a tool call produced for the model to look at.
type attachment struct {
	Path      string
	MediaType string
	Data      string // base64
}

func viewImageDef() llm.Tool {
	return llm.Tool{
		Name: "view_image",
		Description: "Look at an image in /work — the actual picture, not its filename. " +
			"Use it to check that a generated image is what was asked for. " +
			"Supports .png, .jpg, .jpeg, .webp and .gif. The image stays in the conversation afterwards, so view what you need and no more.",
		InputSchema: schema(map[string]string{
			"path": "Path of the image, relative to /work.",
		}, "path"),
	}
}

// viewImage reads an image and queues it for the model. The text result names
// what was attached; the bytes reach the model as an image block, because a
// tool result is a string and an image is not.
func (r *Registry) viewImage(rel string) (string, bool) {
	rel = strings.TrimSpace(rel)
	abs, err := r.resolve(rel)
	if err != nil {
		return err.Error(), true
	}
	ext := strings.ToLower(filepath.Ext(rel))
	mediaType, ok := viewableTypes[ext]
	if !ok {
		if videoExts[ext] {
			return fmt.Sprintf("%s is a video; use view_video, which samples still frames from it.", rel), true
		}
		return fmt.Sprintf("%s is not a viewable image (%s). Viewable: %s.", rel, ext, viewableList()), true
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Sprintf("view_image failed: %v", err), true
	}
	if info.Size() > maxViewBytes {
		return fmt.Sprintf("%s is %s, over the %s limit for viewing. It exists; it is too large to put in the conversation.",
			rel, byteSize(info.Size()), byteSize(maxViewBytes)), true
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Sprintf("view_image failed: %v", err), true
	}
	if len(b) == 0 {
		return fmt.Sprintf("%s is empty", rel), true
	}
	// The copy that travels is re-encoded; the artifact on disk is untouched.
	data, sentAs := forTransport(b, mediaType)
	r.mu.Lock()
	r.attachments = append(r.attachments, attachment{Path: rel, MediaType: sentAs, Data: b64(data)})
	r.mu.Unlock()
	return fmt.Sprintf("attached %s (%s, %s) — the image itself follows this result", rel, sentAs, byteSize(info.Size())), false
}

// TakeAttachments returns the images queued by this turn's tool calls and
// clears them. The loop turns them into image blocks; nothing else may, because
// the same image must not be attached twice.
func (r *Registry) TakeAttachments() []llm.Block {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.attachments) == 0 {
		return nil
	}
	out := make([]llm.Block, 0, len(r.attachments))
	used := 0
	var deferred []string
	for _, a := range r.attachments {
		// base64 is what actually travels, and it is a third larger than the
		// file — measuring the file would under-count the thing being capped.
		size := len(a.Data)
		if used > 0 && used+size > maxTurnAttachBytes {
			deferred = append(deferred, a.Path)
			continue
		}
		used += size
		out = append(out, llm.ImageBlock(a.MediaType, a.Data))
	}
	// Say what was held back, or the model sees fewer pictures than it asked
	// for with no way to know which are missing.
	if len(deferred) > 0 {
		out = append(out, llm.TextBlock(fmt.Sprintf(
			"Not attached, to keep this turn's request within its size limit: %s. View them in a later turn, one or two at a time.",
			strings.Join(deferred, ", "))))
	}
	r.attachments = nil
	return out
}

// b64 is the encoding every dialect takes an image in.
func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// atoiOr parses a small positive integer from a tool argument, falling back
// rather than failing: a model that sends "3 frames" should still get frames.
func atoiOr(s string, def int) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return def
	}
	return n
}

func viewableList() string {
	exts := make([]string, 0, len(viewableTypes))
	for e := range viewableTypes {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}
