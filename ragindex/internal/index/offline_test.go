package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineTokensHandlesJapaneseAndLatin(t *testing.T) {
	got := offlineTokens("冪等キー idempotency-key 24h")
	joined := strings.Join(got, ",")
	for _, want := range []string{"冪等", "等キ", "idempotency", "key", "24h"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing token %q in %v", want, got)
		}
	}
	// A lone latin letter carries no signal and would only add collisions.
	for _, tok := range offlineTokens("a bb") {
		if tok == "a" {
			t.Errorf("single latin letter should not be a token: %v", got)
		}
	}
}

func TestOfflineEmbedIsDeterministicAndNormalised(t *testing.T) {
	a := offlineEmbed([]string{"冪等キーは必須です"})[0]
	b := offlineEmbed([]string{"冪等キーは必須です"})[0]
	if cosine(a, b) < 0.999 {
		t.Errorf("same text embedded twice should be identical, cosine = %v", cosine(a, b))
	}
	if len(a) != offlineDims {
		t.Errorf("dims = %d, want %d", len(a), offlineDims)
	}
	if c := cosine(a, a); c < 0.999 || c > 1.001 {
		t.Errorf("self-cosine = %v, want ~1 (vector should be L2-normalised)", c)
	}
	if all := offlineEmbed([]string{""})[0]; cosine(all, all) != 0 {
		t.Errorf("empty text should embed to the zero vector")
	}
}

// The point of the offline mode is that the graph has real structure: texts
// sharing vocabulary must land nearer each other than unrelated ones. It is an
// approximation, so this asserts ordering, not any particular score.
func TestOfflineEmbedRanksSharedVocabularyHigher(t *testing.T) {
	v := offlineEmbed([]string{
		"決済の冪等キーは24時間保持され、再送は最初の応答を返します",
		"冪等キーの重複は台帳のUNIQUE制約で拒否されます",
		"倉庫システムからの実在庫の取り込みは15分間隔です",
	})
	related, unrelated := cosine(v[0], v[1]), cosine(v[0], v[2])
	if related <= unrelated {
		t.Errorf("shared-vocabulary cosine %v should exceed unrelated %v", related, unrelated)
	}
}

// End to end with no gateway at all: an index builds and answers a search.
// This is the path `ORCHESTRA_EMBED_MODE=offline` takes.
func TestOfflineModeBuildsAndSearchesWithoutAGateway(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("retry.md", "リトライは指数バックオフで最大3回。4xxは再試行しない。")
	write("inventory.md", "引当は30分で自動解放される。安全在庫はSKUごとに設定する。")

	idx := New(Config{Root: root, EmbedMode: EmbedModeOffline}) // no Gateway set at all
	if err := idx.Build(context.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	st := idx.Status()
	if st.Chunks != 2 {
		t.Fatalf("chunks = %d, want 2", st.Chunks)
	}
	if st.EmbedMode != EmbedModeOffline {
		t.Errorf("status embedMode = %q, want %q — the UI has no other way to know", st.EmbedMode, EmbedModeOffline)
	}
	res, err := idx.Search(context.Background(), "再試行の回数はいくつですか", 1, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].Source != "retry.md" {
		t.Fatalf("top result = %+v, want retry.md", res)
	}
}

// A default-configured index must not quietly become offline.
func TestDefaultStatusReportsGatewayMode(t *testing.T) {
	if m := New(Config{}).Status().EmbedMode; m != EmbedModeGateway {
		t.Errorf("default embedMode = %q, want %q", m, EmbedModeGateway)
	}
}
