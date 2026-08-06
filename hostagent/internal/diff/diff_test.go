package diff

import "testing"

const sample = `diff --git a/src/indexer.ts b/src/indexer.ts
index 111..222 100644
--- a/src/indexer.ts
+++ b/src/indexer.ts
@@ -18,4 +18,6 @@ async function rebuildIndex()
   const docs = await db.fetchAll();
-  await index.bulk(docs);
+  const batched = chunk(docs, 500);
+  for (const p of batched) await index.bulk(p);
   await db.commit();
`

func TestParse(t *testing.T) {
	files := Parse(sample)
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	f := files[0]
	if f.Path != "src/indexer.ts" {
		t.Errorf("path = %q", f.Path)
	}
	if f.Additions != 2 || f.Deletions != 1 {
		t.Errorf("add/del = %d/%d, want 2/1", f.Additions, f.Deletions)
	}

	var hunks, adds, dels, ctx int
	for _, l := range f.Lines {
		switch l.Type {
		case Hunk:
			hunks++
		case Add:
			adds++
			if l.NewNo == nil || l.OldNo != nil {
				t.Errorf("add line should have only NewNo: %+v", l)
			}
		case Del:
			dels++
			if l.OldNo == nil || l.NewNo != nil {
				t.Errorf("del line should have only OldNo: %+v", l)
			}
		case Context:
			ctx++
			if l.OldNo == nil || l.NewNo == nil {
				t.Errorf("context line needs both line numbers: %+v", l)
			}
		}
	}
	if hunks != 1 || adds != 2 || dels != 1 || ctx != 2 {
		t.Errorf("counts hunks=%d adds=%d dels=%d ctx=%d", hunks, adds, dels, ctx)
	}

	// first context line starts at old/new 18
	first := f.Lines[1]
	if *first.OldNo != 18 || *first.NewNo != 18 {
		t.Errorf("first context line numbers = %d/%d, want 18/18", *first.OldNo, *first.NewNo)
	}
}

func TestParseEmpty(t *testing.T) {
	if got := Parse(""); len(got) != 0 {
		t.Errorf("empty diff => %d files, want 0", len(got))
	}
}

// Files with no hunks (binary, rename-only) still create a File entry. They must
// carry a name and a non-nil Lines — the frontend keys tabs by .path and maps
// over .lines in render, so a nil there took down the whole app.
func TestParseFilesWithoutHunks(t *testing.T) {
	const noHunks = `diff --git a/assets/logo.png b/assets/logo.png
index 111..222 100644
Binary files a/assets/logo.png and b/assets/logo.png differ
diff --git a/old/name.ts b/new/name.ts
similarity index 100%
rename from old/name.ts
rename to new/name.ts
`
	files := Parse(noHunks)
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	for _, want := range []string{"assets/logo.png", "new/name.ts"} {
		found := false
		for _, f := range files {
			if f.Path == want {
				found = true
				if f.Lines == nil {
					t.Errorf("%s: Lines is nil, want empty slice", want)
				}
			}
		}
		if !found {
			t.Errorf("missing file %q; got %+v", want, files)
		}
	}
}

// A deleted file's "+++ /dev/null" must not become the displayed path.
func TestParseDeletedFileKeepsPath(t *testing.T) {
	const deleted = `diff --git a/gone.txt b/gone.txt
deleted file mode 100644
--- a/gone.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-one
-two
`
	files := Parse(deleted)
	if len(files) != 1 || files[0].Path != "gone.txt" {
		t.Fatalf("path = %+v, want gone.txt", files)
	}
	if files[0].Deletions != 2 {
		t.Errorf("deletions = %d, want 2", files[0].Deletions)
	}
}

// An empty diff must marshal as [] rather than null.
func TestParseEmptyDiffIsNonNil(t *testing.T) {
	if got := Parse(""); got == nil {
		t.Fatal("Parse(\"\") = nil, want empty slice")
	}
}
