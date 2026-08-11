// Media generation — how an agent produces something other than text.
//
// This file used to BE the mechanism: three hand-written tools, each with a
// hardcoded request body and response parser in one vendor's shape. Anything
// spelled differently — Imagen's `instances`/`parameters`, a provider that
// returns its bytes under another key — could not be reached without editing
// this file, and the route was not even separately configurable.
//
// It is now an adapter. The three kinds compile to ordinary artifact tools
// (see artifact.go), which is the same mechanism a user-authored tool uses:
// gateway route, injected key, templated body, an output binding that says
// where the bytes are. What survives here is the part that was always worth
// keeping — the schemas the model sees, the per-kind extension whitelist, and
// the deliberate separation of the three grants, which differ by an order of
// magnitude in cost.
//
// ORCHESTRA_MEDIA therefore remains a supported input: a schedule compiled
// before generation became a tool still runs. New templates express the same
// thing as tool definitions, and get to change the shape.
package tools

import (
	"orchestra/agent/internal/llm"
	"strings"
)

// MediaSpec configures one generation tool. Prefix is a gateway route
// ("/openai/"), so the upstream, its key and its allowlist stay the gateway's
// business; the agent only knows a path.
type MediaSpec struct {
	Prefix string `json:"prefix"`
	Model  string `json:"model"`
	Path   string `json:"path"`   // request path after the prefix; per-kind default below
	Voice  string `json:"voice"`  // speech: default voice
	Size   string `json:"size"`   // image: default size, e.g. "1024x1024"
	Format string `json:"format"` // speech: mp3 | wav | opus | flac (default mp3)
	// Seconds is the video default duration. Providers differ on what they
	// accept, so it is passed through rather than validated here.
	Seconds string `json:"seconds"`
}

// MediaConfig is the ORCHESTRA_MEDIA payload: one entry per enabled kind.
type MediaConfig struct {
	Image  *MediaSpec `json:"image"`
	Speech *MediaSpec `json:"speech"`
	Video  *MediaSpec `json:"video"`
}

// Extensions each kind may write. This is not about the model — it is about
// what the artifact galleries can classify and what the host will serve inline.
var mediaExts = map[string][]string{
	"image":  {".png", ".jpg", ".jpeg", ".webp"},
	"speech": {".mp3", ".wav", ".opus", ".flac"},
	"video":  {".mp4", ".webm"},
}

// SetMedia enables the generation tools described by cfg, by registering them
// as artifact tools. gateway/gctx are the same ones the HTTP tools use.
func (r *Registry) SetMedia(gateway string, gctx llm.GatewayCtx, cfg MediaConfig) {
	r.gateway = strings.TrimRight(gateway, "/")
	r.gctx = gctx
	for _, t := range mediaTools(cfg) {
		r.httpTools[t.Name] = t
	}
}

// mediaTools compiles a MediaConfig into the equivalent artifact tools.
func mediaTools(cfg MediaConfig) []HTTPTool {
	var out []HTTPTool
	if s := cfg.Image; s != nil {
		out = append(out, HTTPTool{
			Name: "generate_image",
			Description: "Generate an image from a text prompt and write it into /work. " +
				"path must end in .png, .jpg, .jpeg or .webp.",
			InputSchema: schema(map[string]string{
				"prompt": "What the image should show. Be specific; this is the only instruction the image model gets.",
				"size":   `Optional pixel size, e.g. "1024x1024".`,
			}, "prompt"),
			Method:   "POST",
			Path:     s.Prefix + specPath(s.Path, "/v1/images/generations"),
			Defaults: map[string]string{"size": s.Size},
			Body: jsonBody(map[string]string{
				"model":  s.Model,
				"prompt": "{{prompt}}",
				"size":   "{{size}}",
			}, `"n":1`),
			Output: &ToolOutput{Kind: "base64", JSONPath: "data.0.b64_json", Extensions: mediaExts["image"]},
		})
	}
	if s := cfg.Speech; s != nil {
		out = append(out, HTTPTool{
			Name: "generate_speech",
			Description: "Synthesise speech from text and write the audio into /work. " +
				"path must end in .mp3, .wav, .opus or .flac.",
			InputSchema: schema(map[string]string{
				"text":  "The text to speak.",
				"voice": "Optional voice name, provider-specific.",
			}, "text"),
			Method:   "POST",
			Path:     s.Prefix + specPath(s.Path, "/v1/audio/speech"),
			Defaults: map[string]string{"voice": s.Voice},
			Body: jsonBody(map[string]string{
				"model": s.Model,
				"input": "{{text}}",
				"voice": "{{voice}}",
				// The audio format is the one the file is being written as: the
				// extension is already validated, so letting the provider be
				// told something else only invites a .wav holding an mp3.
				"response_format": firstNonEmpty("{{ext}}", s.Format),
			}),
			// Speech routes answer with the audio itself.
			Output: &ToolOutput{Kind: "binary", Extensions: mediaExts["speech"]},
		})
	}
	if s := cfg.Video; s != nil {
		out = append(out, HTTPTool{
			Name: "generate_video",
			Description: "Generate a video from a text prompt and write it into /work. " +
				"path must end in .mp4 or .webm. This takes minutes and is the most expensive tool available — use it once, deliberately.",
			InputSchema: schema(map[string]string{
				"prompt":  "What the video should show.",
				"seconds": "Optional duration in seconds.",
			}, "prompt"),
			Method:   "POST",
			Path:     s.Prefix + specPath(s.Path, "/v1/videos"),
			Defaults: map[string]string{"seconds": s.Seconds},
			Body: jsonBody(map[string]string{
				"model":   s.Model,
				"prompt":  "{{prompt}}",
				"seconds": "{{seconds}}",
			}),
			Output: &ToolOutput{
				Kind:       "binary",
				Extensions: mediaExts["video"],
				Poll: &ToolPoll{
					IDPath: "id", StatusPath: "status",
					Done: []string{"completed"}, Fail: []string{"failed", "cancelled"},
					ErrorPath: "error",
					StatusURL: "/{{id}}", ResultURL: "/{{id}}/content",
				},
			},
		})
	}
	return out
}

func specPath(v, def string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

// jsonBody renders a flat JSON object template. A field whose value is empty is
// omitted outright; a field left as an unfilled {{placeholder}} is dropped at
// call time by pruneBody, so an optional argument the model skips is not sent
// as an empty string.
func jsonBody(fields map[string]string, extra ...string) string {
	var parts []string
	for _, k := range sortedKeys(fields) {
		if strings.TrimSpace(fields[k]) == "" {
			continue
		}
		parts = append(parts, `"`+k+`":`+quoteJSON(fields[k]))
	}
	parts = append(parts, extra...)
	return "{" + strings.Join(parts, ",") + "}"
}
