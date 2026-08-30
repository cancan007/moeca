package index

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Describing a picture so it can be found by what is in it.
//
// A knowledge base holds screenshots, diagrams, reference art. Until now none of
// it was searchable: an image is indexed by its path and filename, which finds
// it if you already know what it is called and never otherwise. Captioning
// closes that — a vision model looks at the file once and writes what it shows,
// and that description is indexed as the image's text.
//
// It is opt-in and stays opt-in. A caption costs a model call per picture, and a
// knowledge base of a few hundred screenshots would quietly spend real money the
// first time someone registered the folder. Nothing here runs unless an operator
// sets a caption model.
//
// The cost is paid once per picture, not once per rebuild. Captions are cached
// by the CONTENT of the file, so a reindex re-reads and re-chunks everything
// (cheap, local) while paying only for images it has never seen. Keying on
// content rather than path means a replaced image is re-described and a renamed
// one is not — which is the behaviour you would ask for if asked.
//
// What is indexed is the descriptor AND the caption, not the caption alone. The
// descriptor is what makes a file findable by name, and a caption that replaced
// it would trade one kind of search for another rather than adding one.
//
// Video is NOT captioned, and the reason is the same one that kept it
// metadata-only before: describing a video means sampling frames, which means
// ffmpeg, which means an untrusted-binary parser inside the one container that
// holds all of the knowledge. Captions for video come in as subtitle sidecars,
// which are text and need no parser at all.

const (
	// captionMaxBytes bounds the picture sent to the model. Well above a
	// screenshot and below anything that would be a surprising upload.
	captionMaxBytes = 12 << 20
	// captionCacheFile is the name inside the configured cache directory.
	captionCacheFile = "captions.json"
	// captionTimeout bounds one description. A vision call is seconds; this is
	// slack, not a budget.
	captionTimeout = 90 * time.Second
	// captionPromptVersion changes when the instruction below changes, so old
	// captions are re-taken rather than silently mixed with new ones.
	captionPromptVersion = "1"
)

// captionPrompt asks for what a search will actually be matched against.
//
// Written for retrieval, not for a reader: someone looking for this image will
// describe what is in it, so the caption has to contain those words — subjects,
// text visible in the picture, colours, setting, kind of image. Prose about mood
// would embed further from every query that matters.
const captionPrompt = `この画像を検索インデックス用に説明してください。読み物ではなく検索のための記述です。
含めるもの: 写っている主要な被写体や物体、画像内に読み取れる文字(そのまま書き写す)、色・構図・背景、画像の種類(写真/スクリーンショット/図表/イラストなど)。
含めないもの: 感想、推測、前置き、「この画像は」のような枕詞。
日本語で、200文字程度の平文で書いてください。`

// captionEnabled reports whether an operator has turned this on.
func (i *Index) captionEnabled() bool {
	return strings.TrimSpace(i.cfg.CaptionModel) != ""
}

// captionPrefixOr returns the gateway route the vision model sits behind,
// falling back to the embedding route since one provider usually serves both.
func (i *Index) captionPrefixOr() string {
	if p := strings.TrimSpace(i.cfg.CaptionPrefix); p != "" {
		return p
	}
	return i.cfg.EmbedPrefix
}

// captionStore is the content-hash → caption cache, in memory and on disk.
type captionStore struct {
	mu sync.RWMutex
	m  map[string]string
}

func (c *captionStore) get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.m[key]
	return v, ok
}

func (c *captionStore) put(key, val string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]string{}
	}
	c.m[key] = val
}

func (c *captionStore) snapshot() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]string, len(c.m))
	for k, v := range c.m {
		out[k] = v
	}
	return out
}

// captionCachePath returns where captions are kept, or "" when no cache
// directory is configured — in which case they last as long as the process.
func (i *Index) captionCachePath() string {
	if i.cfg.CacheDir == "" {
		return ""
	}
	return filepath.Join(i.cfg.CacheDir, captionCacheFile)
}

// LoadCaptions restores captions taken before the last restart. A missing or
// damaged file simply means the next build pays for them again.
func (i *Index) LoadCaptions() {
	path := i.captionCachePath()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("ragindex: reading caption cache: %v", err)
		}
		return
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		log.Printf("ragindex: ignoring damaged caption cache: %v", err)
		return
	}
	i.captions.mu.Lock()
	i.captions.m = m
	i.captions.mu.Unlock()
	log.Printf("ragindex: restored %d cached caption(s)", len(m))
}

// saveCaptions writes the cache so the next build does not re-describe what it
// has already seen. Temp-and-rename, so a crash mid-write costs nothing beyond
// the captions taken since the last save.
func (i *Index) saveCaptions() {
	path := i.captionCachePath()
	if path == "" {
		return
	}
	raw, err := json.Marshal(i.captions.snapshot())
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("ragindex: writing caption cache: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("ragindex: replacing caption cache: %v", err)
		os.Remove(tmp)
	}
}

// captionKey identifies a caption by what was described and by what described
// it. The model and the prompt are in the key because a caption from another
// model answering another question is not the same caption, and reusing it
// would leave an index whose descriptions silently disagree with each other.
func captionKey(content []byte, model, promptVersion string) string {
	sum := sha256.Sum256(content)
	h := sha256.Sum256([]byte(hex.EncodeToString(sum[:]) + "\x00" + model + "\x00" + promptVersion))
	return hex.EncodeToString(h[:])
}

// captionImage returns a description of one image file, from the cache when it
// has seen those bytes before and from the model otherwise.
//
// A failure is returned for the caller to note, never to fail a build with: an
// image nobody could describe should still be in the index by name, which is
// exactly where it was before this existed.
func (i *Index) captionImage(ctx context.Context, path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > captionMaxBytes {
		return "", fmt.Errorf("画像が上限(%d MiB)を超えるため説明を生成しません", captionMaxBytes>>20)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := captionKey(b, i.cfg.CaptionModel, captionPromptVersion)
	if cached, ok := i.captions.get(key); ok {
		return cached, nil
	}
	text, err := i.describeViaGateway(ctx, b, path)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("モデルが空の説明を返しました")
	}
	i.captions.put(key, text)
	return text, nil
}

// describeViaGateway asks the vision model to describe one image.
//
// The chat-completions shape, for the same reason embedViaGateway uses the
// embeddings shape: this container talks to one route the gateway defines, and
// the provider behind it holds the key. A provider that spells this differently
// is a change here rather than a credential anywhere near the knowledge.
func (i *Index) describeViaGateway(ctx context.Context, img []byte, path string) (string, error) {
	ctype := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if ctype == "" {
		ctype = "image/png"
	}
	dataURI := "data:" + ctype + ";base64," + base64.StdEncoding.EncodeToString(img)

	body, _ := json.Marshal(map[string]any{
		"model": i.cfg.CaptionModel,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": captionPrompt},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURI}},
			},
		}},
	})
	url := strings.TrimRight(i.cfg.Gateway, "/") + i.captionPrefixOr() + "/v1/chat/completions"

	ctx, cancel := context.WithTimeout(ctx, captionTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if i.cfg.Session != "" {
		req.Header.Set("X-Orchestra-Session", i.cfg.Session)
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("caption request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("caption %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("caption decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("caption response carried no choices")
	}
	return out.Choices[0].Message.Content, nil
}

// captionerFor returns the describe function ingestion should use, or nil when
// captioning is off — which is what makes "off" cost nothing rather than cost a
// check per file.
func (i *Index) captionerFor(ctx context.Context) captioner {
	if !i.captionEnabled() {
		return nil
	}
	return func(path string) (string, error) { return i.captionImage(ctx, path) }
}
