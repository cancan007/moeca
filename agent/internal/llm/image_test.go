package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every dialect can take an image in a user turn, but each spells it
// differently. The neutral block is encoded per dialect so that "look at what
// you produced" works whichever provider an agent is bound to.

func userWithImage() []Message {
	return []Message{{Role: "user", Content: []Block{
		TextBlock("here it is"),
		ImageBlock("image/png", "QUJD"),
	}}}
}

func TestAnthropicSendsAnImageBlock(t *testing.T) {
	raw, err := json.Marshal(Request{Model: "m", MaxTokens: 10, Messages: userWithImage()})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{`"type":"image"`, `"media_type":"image/png"`, `"data":"QUJD"`, `"type":"base64"`} {
		if !strings.Contains(s, want) {
			t.Errorf("anthropic request missing %s:\n%s", want, s)
		}
	}
}

func TestOpenAISendsADataURI(t *testing.T) {
	c := &openAIClient{}
	raw, err := json.Marshal(c.encode(Request{Model: "gpt-4o", MaxTokens: 10, Messages: userWithImage()}))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"image_url"`) || !strings.Contains(s, "data:image/png;base64,QUJD") {
		t.Errorf("openai request does not carry the image:\n%s", s)
	}
	// The text belongs in the same message, as a part.
	if !strings.Contains(s, `"type":"text"`) {
		t.Errorf("the accompanying text was dropped:\n%s", s)
	}
}

// A request with no image keeps the plain-string content shape: the array form
// would change every request for the sake of the rare one carrying a picture.
func TestOpenAIKeepsPlainContentWithoutAnImage(t *testing.T) {
	c := &openAIClient{}
	raw, _ := json.Marshal(c.encode(Request{Model: "gpt-4o", MaxTokens: 10, Messages: []Message{
		{Role: "user", Content: []Block{TextBlock("hello")}},
	}}))
	if !strings.Contains(string(raw), `"content":"hello"`) {
		t.Errorf("content is no longer a plain string:\n%s", raw)
	}
}

func TestGeminiSendsInlineData(t *testing.T) {
	c := &geminiClient{}
	raw, err := json.Marshal(c.encode(Request{Model: "gemini", MaxTokens: 10, Messages: userWithImage()}))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"inlineData"`) || !strings.Contains(s, `"mimeType":"image/png"`) || !strings.Contains(s, `"data":"QUJD"`) {
		t.Errorf("gemini request does not carry the image:\n%s", s)
	}
}
