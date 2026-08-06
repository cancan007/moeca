// Media generation — how a task produces something other than text.
//
// The file tools write strings, so until now an agent could only ever produce
// text. A Daily schedule that is supposed to render a chart, or a Delivery task
// that is supposed to ship a diagram, had no way to put bytes on disk.
//
// These three tools generate an image, a spoken track or a video and write the
// result into /work, where Daily's gallery and Delivery's artifact tab both
// pick it up. Every call goes THROUGH the gateway on the same terms as
// everything else: the agent sends no credentials, the gateway injects them,
// the allowlist and budget apply, and the request lands in the audit log with
// its run and stage attached. Generation is a model call like any other — it
// gets no special path out.
//
// Which of the three exist is configuration (ORCHESTRA_MEDIA), not a constant.
// An agent template that has no business generating video does not get a video
// tool, and a model that cannot be reached is better represented by an absent
// tool than by one that always fails.
package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"orchestra/agent/internal/llm"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MediaSpec configures one generation tool. Prefix is a gateway route
// ("/openai"), so the upstream, its key and its allowlist stay the gateway's
// business; the agent only knows a path.
//
// Paths are overridable because "the OpenAI shape" is a convention, not a
// standard — a compatible provider that spells the route differently should be
// a config change, not a patch.
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
// A generated file that lands as `.sh` is not an artifact, it is a foothold, so
// the tool refuses rather than trusting the extension the model picked.
var mediaExts = map[string]map[string]bool{
	"image":  {".png": true, ".jpg": true, ".jpeg": true, ".webp": true},
	"speech": {".mp3": true, ".wav": true, ".opus": true, ".flac": true},
	"video":  {".mp4": true, ".webm": true},
}

// Generation is slow — an image is seconds, a video is minutes — so these get
// their own client rather than the 30s one the HTTP tools share.
const (
	mediaCallTimeout = 10 * time.Minute
	videoPollFor     = 15 * time.Minute
	maxMediaBytes    = 256 << 20
)

// videoPollEvery is a var only so tests can shorten it; production never
// changes it.
var videoPollEvery = 5 * time.Second

// SetMedia enables the generation tools described by cfg. gateway/gctx are the
// same ones the HTTP tools use; passing them again keeps this independent of
// whether custom HTTP tools were configured at all.
func (r *Registry) SetMedia(gateway string, gctx llm.GatewayCtx, cfg MediaConfig) {
	r.gateway = strings.TrimRight(gateway, "/")
	r.gctx = gctx
	r.media = &cfg
	r.mediaHTTP = &http.Client{Timeout: mediaCallTimeout}
}

func (s *MediaSpec) pathOr(def string) string {
	if s.Path != "" {
		return s.Path
	}
	return def
}

// mediaDefinitions returns the tool definitions for whichever kinds are
// configured. Each takes an explicit output path: the model has to say where
// the artifact goes, and the caller reviewing it can see the name it chose.
func (r *Registry) mediaDefinitions() []llm.Tool {
	if r.media == nil {
		return nil
	}
	var defs []llm.Tool
	if r.media.Image != nil {
		defs = append(defs, llm.Tool{
			Name: "generate_image",
			Description: "Generate an image from a text prompt and write it into /work. " +
				"path must end in .png, .jpg, .jpeg or .webp.",
			InputSchema: schema(map[string]string{
				"prompt": "What the image should show. Be specific; this is the only instruction the image model gets.",
				"path":   `Output path relative to /work, e.g. "artifacts/chart.png".`,
				"size":   `Optional pixel size, e.g. "1024x1024".`,
			}, "prompt", "path"),
		})
	}
	if r.media.Speech != nil {
		defs = append(defs, llm.Tool{
			Name: "generate_speech",
			Description: "Synthesise speech from text and write the audio into /work. " +
				"path must end in .mp3, .wav, .opus or .flac.",
			InputSchema: schema(map[string]string{
				"text":  "The text to speak.",
				"path":  `Output path relative to /work, e.g. "artifacts/summary.mp3".`,
				"voice": "Optional voice name, provider-specific.",
			}, "text", "path"),
		})
	}
	if r.media.Video != nil {
		defs = append(defs, llm.Tool{
			Name: "generate_video",
			Description: "Generate a video from a text prompt and write it into /work. " +
				"path must end in .mp4 or .webm. This takes minutes and is the most expensive tool available — use it once, deliberately.",
			InputSchema: schema(map[string]string{
				"prompt":  "What the video should show.",
				"path":    `Output path relative to /work, e.g. "artifacts/demo.mp4".`,
				"seconds": "Optional duration in seconds, provider-specific.",
			}, "prompt", "path"),
		})
	}
	return defs
}

// dispatchMedia handles the generation tools. The bool is the "is error"
// convention shared with the other tools.
func (r *Registry) dispatchMedia(name string, args map[string]any) (string, bool, bool) {
	if r.media == nil {
		return "", false, false
	}
	switch name {
	case "generate_image":
		if r.media.Image == nil {
			return "", false, false
		}
		out, isErr := r.generateImage(args)
		return out, isErr, true
	case "generate_speech":
		if r.media.Speech == nil {
			return "", false, false
		}
		out, isErr := r.generateSpeech(args)
		return out, isErr, true
	case "generate_video":
		if r.media.Video == nil {
			return "", false, false
		}
		out, isErr := r.generateVideo(args)
		return out, isErr, true
	}
	return "", false, false
}

func (r *Registry) generateImage(args map[string]any) (string, bool) {
	spec := r.media.Image
	dest, err := r.mediaDest(str(args, "path"), "image")
	if err != nil {
		return err.Error(), true
	}
	prompt := strings.TrimSpace(str(args, "prompt"))
	if prompt == "" {
		return "prompt is required", true
	}
	body := map[string]any{"model": spec.Model, "prompt": prompt, "n": 1}
	if size := firstNonEmpty(str(args, "size"), spec.Size); size != "" {
		body["size"] = size
	}
	raw, ct, err := r.mediaPost(spec.Prefix+spec.pathOr("/v1/images/generations"), body)
	if err != nil {
		return err.Error(), true
	}

	// A compatible provider may answer with the bytes directly or with the
	// documented JSON envelope; accept both rather than making the caller care.
	data := raw
	if strings.Contains(ct, "json") {
		var out struct {
			Data []struct {
				B64 string `json:"b64_json"`
				URL string `json:"url"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Data) == 0 {
			return "image response could not be read: " + truncate(string(raw), 400), true
		}
		if out.Data[0].B64 == "" {
			// A URL would mean a second fetch to a host the gateway has not
			// allowlisted, which is exactly the egress this design forbids.
			return "provider returned a URL instead of image bytes; configure it to return b64_json", true
		}
		if data, err = base64.StdEncoding.DecodeString(out.Data[0].B64); err != nil {
			return "image was not valid base64: " + err.Error(), true
		}
	}
	return writeArtifact(dest, str(args, "path"), data)
}

func (r *Registry) generateSpeech(args map[string]any) (string, bool) {
	spec := r.media.Speech
	rel := str(args, "path")
	dest, err := r.mediaDest(rel, "speech")
	if err != nil {
		return err.Error(), true
	}
	text := strings.TrimSpace(str(args, "text"))
	if text == "" {
		return "text is required", true
	}
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(rel)), ".")
	if spec.Format != "" && format == "" {
		format = spec.Format
	}
	body := map[string]any{"model": spec.Model, "input": text, "response_format": format}
	if voice := firstNonEmpty(str(args, "voice"), spec.Voice); voice != "" {
		body["voice"] = voice
	}
	raw, ct, err := r.mediaPost(spec.Prefix+spec.pathOr("/v1/audio/speech"), body)
	if err != nil {
		return err.Error(), true
	}
	if strings.Contains(ct, "json") {
		// Speech routes answer with audio; JSON here means an error envelope
		// that came back with a 200, which does happen.
		return "speech response was JSON, not audio: " + truncate(string(raw), 400), true
	}
	return writeArtifact(dest, rel, raw)
}

// generateVideo creates a job, waits for it, then downloads it. Video is the
// one kind that is asynchronous everywhere, so unlike the other two this holds
// the tool call open across a poll loop rather than returning a job id the
// model would have to remember to check.
func (r *Registry) generateVideo(args map[string]any) (string, bool) {
	spec := r.media.Video
	rel := str(args, "path")
	dest, err := r.mediaDest(rel, "video")
	if err != nil {
		return err.Error(), true
	}
	prompt := strings.TrimSpace(str(args, "prompt"))
	if prompt == "" {
		return "prompt is required", true
	}
	base := spec.Prefix + spec.pathOr("/v1/videos")
	body := map[string]any{"model": spec.Model, "prompt": prompt}
	if secs := firstNonEmpty(str(args, "seconds"), spec.Seconds); secs != "" {
		body["seconds"] = secs
	}
	raw, _, err := r.mediaPost(base, body)
	if err != nil {
		return err.Error(), true
	}
	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  any    `json:"error"`
	}
	if err := json.Unmarshal(raw, &job); err != nil || job.ID == "" {
		return "video job response could not be read: " + truncate(string(raw), 400), true
	}

	deadline := time.Now().Add(videoPollFor)
	for job.Status != "completed" {
		if job.Status == "failed" || job.Status == "cancelled" {
			return fmt.Sprintf("video job %s: %s", job.ID, truncate(fmt.Sprint(job.Error), 300)), true
		}
		if time.Now().After(deadline) {
			return fmt.Sprintf("video job %s did not finish within %s (last status %q)", job.ID, videoPollFor, job.Status), true
		}
		time.Sleep(videoPollEvery)
		poll, _, err := r.mediaGet(base + "/" + job.ID)
		if err != nil {
			return err.Error(), true
		}
		if err := json.Unmarshal(poll, &job); err != nil {
			return "video status could not be read: " + truncate(string(poll), 400), true
		}
	}

	data, ct, err := r.mediaGet(base + "/" + job.ID + "/content")
	if err != nil {
		return err.Error(), true
	}
	if strings.Contains(ct, "json") {
		return "video download returned JSON, not video: " + truncate(string(data), 400), true
	}
	return writeArtifact(dest, rel, data)
}

/* ── plumbing ── */

// mediaDest validates the output path for a kind and returns the absolute path.
func (r *Registry) mediaDest(rel, kind string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("path is required")
	}
	ext := strings.ToLower(filepath.Ext(rel))
	if !mediaExts[kind][ext] {
		return "", fmt.Errorf("path %q must end in one of %s for %s output", rel, extList(kind), kind)
	}
	return r.resolve(rel)
}

func (r *Registry) mediaPost(path string, body map[string]any) ([]byte, string, error) {
	raw, _ := json.Marshal(body)
	return r.mediaDo(http.MethodPost, path, bytes.NewReader(raw))
}

func (r *Registry) mediaGet(path string) ([]byte, string, error) {
	return r.mediaDo(http.MethodGet, path, nil)
}

func (r *Registry) mediaDo(method, path string, body io.Reader) ([]byte, string, error) {
	if r.mediaHTTP == nil || r.gateway == "" {
		return nil, "", fmt.Errorf("media generation is not configured")
	}
	req, err := http.NewRequest(method, r.gateway+path, body)
	if err != nil {
		return nil, "", fmt.Errorf("bad request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	r.gctx.Apply(req.Header)
	resp, err := r.mediaHTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMediaBytes))
	if err != nil {
		return nil, "", fmt.Errorf("read failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 400))
	}
	return raw, resp.Header.Get("Content-Type"), nil
}

func writeArtifact(dest, rel string, data []byte) (string, bool) {
	if len(data) == 0 {
		return "the provider returned no bytes", true
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "could not create directory: " + err.Error(), true
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "could not write file: " + err.Error(), true
	}
	return fmt.Sprintf("wrote %s (%d bytes)", rel, len(data)), false
}

// schema builds the object schema shape the model expects: every property is a
// string, and required names the ones without a default.
func schema(props map[string]string, required ...string) map[string]any {
	p := map[string]any{}
	for name, desc := range props {
		p[name] = map[string]any{"type": "string", "description": desc}
	}
	return map[string]any{"type": "object", "properties": p, "required": required}
}

func extList(kind string) string {
	var out []string
	for e := range mediaExts[kind] {
		out = append(out, e)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
