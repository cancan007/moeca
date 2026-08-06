package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDenyPathsBlocksFileTools(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "id.pem"), []byte("SECRET KEY"), 0o644)
	os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
	os.WriteFile(filepath.Join(root, "secrets", "token.txt"), []byte("t"), 0o644)
	os.WriteFile(filepath.Join(root, "app.ts"), []byte("ok"), 0o644)

	reg := New(root)
	reg.SetDenyPaths([]string{"*.pem", "secrets/*"})

	// read_file on a denied basename glob
	out, isErr := reg.Dispatch("read_file", map[string]any{"path": "id.pem"})
	if !isErr || !strings.Contains(out, "policy") {
		t.Errorf("read id.pem: got (%q, %v), want policy block", out, isErr)
	}
	// even a nested pem is blocked (basename match)
	os.MkdirAll(filepath.Join(root, "keys"), 0o755)
	os.WriteFile(filepath.Join(root, "keys", "server.pem"), []byte("k"), 0o644)
	if out, isErr := reg.Dispatch("read_file", map[string]any{"path": "keys/server.pem"}); !isErr || !strings.Contains(out, "policy") {
		t.Errorf("read keys/server.pem: got (%q, %v), want policy block", out, isErr)
	}
	// write_file into a denied directory glob
	if out, isErr := reg.Dispatch("write_file", map[string]any{"path": "secrets/new.txt", "content": "x"}); !isErr || !strings.Contains(out, "policy") {
		t.Errorf("write secrets/new.txt: got (%q, %v), want policy block", out, isErr)
	}
	// an allowed file still works
	if out, isErr := reg.Dispatch("read_file", map[string]any{"path": "app.ts"}); isErr {
		t.Errorf("read app.ts should succeed, got (%q, %v)", out, isErr)
	}
}
