package index

import (
	"math"
	"testing"
)

// mkIndex builds an index directly from chunks, bypassing embedding: the graph
// is a pure function of the vectors, so the gateway round trip would only make
// these tests slower and less specific.
func mkIndex(chunks []Chunk, sources []Source) *Index {
	i := New(Config{})
	i.chunks, i.sources = chunks, sources
	return i
}

func unit(v ...float32) []float32 {
	var n float32
	for _, x := range v {
		n += x * x
	}
	inv := float32(1 / math.Sqrt(float64(n)))
	out := make([]float32, len(v))
	for k, x := range v {
		out[k] = x * inv
	}
	return out
}

// Two documents about the same thing must come back as each other's nearest
// neighbour, and the unrelated one must not. This is the only claim the edges
// make, so it is the one worth pinning.
func TestNeighboursFollowSimilarityNotOrder(t *testing.T) {
	idx := mkIndex([]Chunk{
		{Source: "a.md", vec: unit(1, 0, 0)},
		{Source: "b.md", vec: unit(0.97, 0.24, 0)},
		{Source: "z.md", vec: unit(0, 0, 1)},
	}, []Source{
		{Path: "a.md", Kind: KindLocal, Chunks: 1},
		{Path: "b.md", Kind: KindLocal, Chunks: 1},
		{Path: "z.md", Kind: KindLocal, Chunks: 1},
	})

	g := idx.Graph()
	if len(g.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(g.Nodes))
	}
	byName := map[string]GraphNode{}
	for _, n := range g.Nodes {
		byName[n.Source] = n
	}
	nearest := func(name string) string { return g.Nodes[byName[name].Near[0].To].Source }
	if got := nearest("a.md"); got != "b.md" {
		t.Errorf("a.md's nearest = %q, want b.md", got)
	}
	if got := nearest("b.md"); got != "a.md" {
		t.Errorf("b.md's nearest = %q, want a.md", got)
	}
	if s := byName["a.md"].Near[0].Score; s < 0.9 {
		t.Errorf("similar documents scored %.3f, want ≈1", s)
	}
}

// A source's node stands for all its chunks, so the centroid must actually
// average them rather than take the first.
func TestSourceVectorIsTheCentroidOfItsChunks(t *testing.T) {
	// left's two chunks straddle right's single one; averaging puts left next to
	// it, while taking either chunk alone would not.
	idx := mkIndex([]Chunk{
		{Source: "left.md", vec: unit(1, 1, 0)},
		{Source: "left.md", vec: unit(1, -1, 0)},
		{Source: "right.md", vec: unit(1, 0, 0)},
		{Source: "away.md", vec: unit(0, 0, 1)},
	}, nil)

	g := idx.Graph()
	var left GraphNode
	for _, n := range g.Nodes {
		if n.Source == "left.md" {
			left = n
		}
	}
	if got := g.Nodes[left.Near[0].To].Source; got != "right.md" {
		t.Errorf("left.md's nearest = %q, want right.md (centroid, not first chunk)", got)
	}
	if s := left.Near[0].Score; s < 0.99 {
		t.Errorf("centroid similarity = %.4f, want ≈1", s)
	}
}

// The layout must be stable across calls, or nodes would jump every time the
// screen reloaded and the user's mental map would be worthless.
func TestProjectionIsDeterministic(t *testing.T) {
	chunks := []Chunk{
		{Source: "a", vec: unit(1, 0, 0, 0)},
		{Source: "b", vec: unit(0, 1, 0, 0)},
		{Source: "c", vec: unit(0, 0, 1, 0)},
		{Source: "d", vec: unit(0.5, 0.5, 0, 0)},
	}
	first := mkIndex(chunks, nil).Graph()
	second := mkIndex(chunks, nil).Graph()
	for k := range first.Nodes {
		if first.Nodes[k].X != second.Nodes[k].X || first.Nodes[k].Y != second.Nodes[k].Y {
			t.Fatalf("node %d moved between runs: (%v,%v) vs (%v,%v)",
				k, first.Nodes[k].X, first.Nodes[k].Y, second.Nodes[k].X, second.Nodes[k].Y)
		}
	}
}

// Positions are normalised so the viewport decides the scale.
func TestPositionsAreNormalised(t *testing.T) {
	g := mkIndex([]Chunk{
		{Source: "a", vec: unit(1, 0, 0)},
		{Source: "b", vec: unit(0, 1, 0)},
		{Source: "c", vec: unit(0, 0, 1)},
		{Source: "d", vec: unit(1, 1, 1)},
	}, nil).Graph()
	for _, n := range g.Nodes {
		if n.X < 0 || n.X > 1 || n.Y < 0 || n.Y > 1 {
			t.Errorf("%s at (%v,%v) is outside 0..1", n.Source, n.X, n.Y)
		}
	}
}

// The two axes must be independent. If the second component were not deflated
// out of the first, every node would land on a diagonal line and the picture
// would carry one dimension of information while appearing to carry two.
func TestAxesAreNotCollinear(t *testing.T) {
	chunks := []Chunk{}
	// A deliberately two-dimensional arrangement: a grid in the first two dims.
	for a := 0; a < 4; a++ {
		for b := 0; b < 4; b++ {
			chunks = append(chunks, Chunk{
				Source: string(rune('a'+a)) + string(rune('0'+b)),
				vec:    unit(float32(a+1), float32(b+1), 0.1),
			})
		}
	}
	g := mkIndex(chunks, nil).Graph()
	var sx, sy, sxy, sxx, syy float64
	n := float64(len(g.Nodes))
	for _, nd := range g.Nodes {
		sx += nd.X
		sy += nd.Y
		sxy += nd.X * nd.Y
		sxx += nd.X * nd.X
		syy += nd.Y * nd.Y
	}
	num := sxy - sx*sy/n
	den := math.Sqrt((sxx - sx*sx/n) * (syy - sy*sy/n))
	if den == 0 {
		t.Fatal("an axis carried no spread at all")
	}
	if r := math.Abs(num / den); r > 0.9 {
		t.Errorf("axes correlate at r=%.3f — the projection collapsed to a line", r)
	}
}

// One source cannot be spread out; say so instead of drawing a lone dot that
// reads as a broken index.
func TestSingleSourceIsReportedDegenerate(t *testing.T) {
	g := mkIndex([]Chunk{{Source: "only.md", vec: unit(1, 0, 0)}}, nil).Graph()
	if !g.Degenerate {
		t.Error("a one-node graph must be flagged degenerate")
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Near == nil {
		t.Errorf("nodes = %+v, want one node with a non-nil neighbour list", g.Nodes)
	}
}

// Identical documents have no spread to project; centring them beats dividing
// by a vanishing range.
func TestIdenticalSourcesDoNotProduceNaN(t *testing.T) {
	g := mkIndex([]Chunk{
		{Source: "a", vec: unit(1, 0, 0)},
		{Source: "b", vec: unit(1, 0, 0)},
		{Source: "c", vec: unit(1, 0, 0)},
	}, nil).Graph()
	if !g.Degenerate {
		t.Error("identical sources must be flagged degenerate")
	}
	for _, n := range g.Nodes {
		if math.IsNaN(n.X) || math.IsNaN(n.Y) {
			t.Errorf("%s got NaN coordinates", n.Source)
		}
	}
}

// An empty index must serialise as an empty list, not null.
func TestEmptyIndexGraph(t *testing.T) {
	g := mkIndex(nil, nil).Graph()
	if g.Nodes == nil {
		t.Error("nodes must be an empty slice, never nil")
	}
}

// An external source reports a display label as its Path but is keyed by URL in
// the chunks, so metadata must still find it — otherwise every named external
// document would render as an untyped node.
func TestExternalSourceMetadataIsMatchedByURL(t *testing.T) {
	g := mkIndex([]Chunk{
		{Source: "https://example.com/spec", vec: unit(1, 0, 0)},
		{Source: "local.md", vec: unit(0, 1, 0)},
	}, []Source{
		{Path: "AlphaDoc", URL: "https://example.com/spec", Kind: KindExternal, Scope: ScopeOrganization, Chunks: 1, Groups: []string{"g1"}},
		{Path: "local.md", Kind: KindLocal, Scope: ScopeProject, Chunks: 1},
	}).Graph()

	var ext *GraphNode
	for k := range g.Nodes {
		if g.Nodes[k].Kind == KindExternal {
			ext = &g.Nodes[k]
		}
	}
	if ext == nil {
		t.Fatal("external source lost its metadata")
	}
	if ext.Source != "AlphaDoc" || ext.URL != "https://example.com/spec" {
		t.Errorf("external node = %+v, want the display label and its URL", ext)
	}
	if len(ext.Groups) != 1 || ext.Groups[0] != "g1" {
		t.Errorf("groups = %v, want [g1]", ext.Groups)
	}
}

// Chunks embedded by different models have different lengths. Mixing them into
// one centroid would produce nonsense, so they are skipped the way cosine does.
func TestMismatchedVectorLengthsAreSkipped(t *testing.T) {
	g := mkIndex([]Chunk{
		{Source: "a", vec: unit(1, 0, 0)},
		{Source: "a", vec: unit(1, 0, 0, 0)}, // a different model
		{Source: "b", vec: unit(0, 1, 0)},
	}, nil).Graph()
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(g.Nodes))
	}
	for _, n := range g.Nodes {
		if math.IsNaN(n.X) || math.IsNaN(n.Y) {
			t.Errorf("%s got NaN from a mixed-dimension source", n.Source)
		}
	}
}
