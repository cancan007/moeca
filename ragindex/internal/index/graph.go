package index

import (
	"math"
	"sort"
)

// The graph view's data: one node per source, positioned by where its content
// actually sits in embedding space, plus each node's nearest neighbours.
//
// The screen draws two things the index can answer honestly — which documents
// are near which, and how near — so both come from the real vectors rather
// than from a layout heuristic. Everything the user tunes on screen (how many
// neighbours to draw, a similarity floor, mutual-only edges) is a filter over
// this response, so the controls are instant and the index is read once.

// graphNeighbors is how many nearest neighbours each node carries. The UI's
// "top N" control tops out well below this, and sending a fixed depth means
// moving that slider never needs another round trip.
const graphNeighbors = 8

// Neighbor is one edge candidate: the index of the other node and the cosine
// similarity between the two sources' centroids.
type Neighbor struct {
	To    int     `json:"to"`
	Score float64 `json:"score"`
}

// GraphNode is one source, placed.
//
// X and Y are in 0..1 rather than screen units: the projection is a property of
// the data, and how large it is drawn is a property of the viewport.
type GraphNode struct {
	Source string     `json:"source"`
	Kind   string     `json:"kind"`
	Scope  string     `json:"scope"`
	URL    string     `json:"url,omitempty"`
	Groups []string   `json:"groups,omitempty"`
	Chunks int        `json:"chunks"`
	X      float64    `json:"x"`
	Y      float64    `json:"y"`
	Near   []Neighbor `json:"near"`
}

// Graph is the whole projected index.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	// Degenerate reports that the projection collapsed — every source landed in
	// nearly the same place because there is too little variation (or too few
	// sources) to spread them out. The screen should say so rather than draw a
	// dot and let the user conclude the index is broken.
	Degenerate bool `json:"degenerate"`
}

// Graph projects the index for the graph view.
//
// Sources are compared by the centroid of their chunk vectors. That is a
// coarser view than chunk-level retrieval — a document covering two unrelated
// topics lands between them — but the graph is about documents, and showing one
// node per chunk would put thousands of dots on screen with no way to read them.
//
// Positions come from a two-component PCA of those centroids. It is
// deterministic, so the layout is the same on every reload and a node stays
// where the user last saw it; a force simulation would drift. What it costs is
// fidelity: 1536 dimensions flattened to two will overlap clusters that are
// genuinely apart, so proximity on screen is a hint and the edges carry the
// real similarity.
func (i *Index) Graph() Graph {
	i.mu.RLock()
	chunks := i.chunks
	sources := i.sources
	i.mu.RUnlock()

	cent, order := centroids(chunks)
	if len(order) == 0 {
		return Graph{Nodes: []GraphNode{}}
	}

	meta := sourceIndex(sources)
	nodes := make([]GraphNode, len(order))
	for k, key := range order {
		n := GraphNode{Source: key, Kind: KindLocal, Near: []Neighbor{}}
		if s, ok := meta[key]; ok {
			n.Source, n.Kind, n.Scope, n.URL, n.Groups, n.Chunks = s.Path, s.Kind, s.Scope, s.URL, s.Groups, s.Chunks
		}
		nodes[k] = n
	}

	xs, ys, degenerate := project(cent)
	for k := range nodes {
		nodes[k].X, nodes[k].Y = xs[k], ys[k]
	}
	attachNeighbors(nodes, cent)
	return Graph{Nodes: nodes, Degenerate: degenerate}
}

// centroids averages each source's chunk vectors and normalises the result, so
// a later dot product is the cosine. Sources are returned in first-seen order,
// which keeps node indices stable for a given index build.
func centroids(chunks []Chunk) ([][]float32, []string) {
	var order []string
	sums := map[string][]float32{}
	counts := map[string]int{}
	for _, c := range chunks {
		if len(c.vec) == 0 {
			continue
		}
		sum, seen := sums[c.Source]
		if !seen {
			sum = make([]float32, len(c.vec))
			order = append(order, c.Source)
		}
		// A source whose chunks were embedded by different models would have
		// mismatched lengths; skip rather than panic, the same way cosine does.
		if len(sum) != len(c.vec) {
			continue
		}
		for d, v := range c.vec {
			sum[d] += v
		}
		sums[c.Source] = sum
		counts[c.Source]++
	}
	out := make([][]float32, 0, len(order))
	for _, key := range order {
		v := sums[key]
		norm := float32(0)
		for _, x := range v {
			norm += x * x
		}
		if norm > 0 {
			inv := float32(1 / math.Sqrt(float64(norm)))
			for d := range v {
				v[d] *= inv
			}
		}
		out = append(out, v)
	}
	return out, order
}

// sourceIndex lets a chunk's source key find its Source metadata. Local sources
// are keyed by relative path and external ones by URL, but a named external
// source reports a display label as its Path — so both are registered.
func sourceIndex(sources []Source) map[string]Source {
	m := make(map[string]Source, len(sources)*2)
	for _, s := range sources {
		if s.URL != "" {
			m[s.URL] = s
		}
		if s.Path != "" {
			if _, taken := m[s.Path]; !taken {
				m[s.Path] = s
			}
		}
	}
	return m
}

// attachNeighbors fills each node's nearest neighbours by cosine. This is an
// exhaustive N² comparison, which is the right trade at the scale a person can
// read on one screen and keeps the scores exact; an approximate structure would
// buy speed the graph view cannot spend.
func attachNeighbors(nodes []GraphNode, cent [][]float32) {
	n := len(nodes)
	for a := 0; a < n; a++ {
		near := make([]Neighbor, 0, n-1)
		for b := 0; b < n; b++ {
			if a == b {
				continue
			}
			near = append(near, Neighbor{To: b, Score: dot(cent[a], cent[b])})
		}
		sort.Slice(near, func(x, y int) bool {
			if near[x].Score != near[y].Score {
				return near[x].Score > near[y].Score
			}
			return near[x].To < near[y].To // stable output for equal scores
		})
		if len(near) > graphNeighbors {
			near = near[:graphNeighbors]
		}
		nodes[a].Near = near
	}
}

func dot(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// project reduces the centroids to two dimensions by power iteration on the
// first two principal components, then rescales each axis into 0..1.
//
// The second component is found after deflating the first, so the axes are
// orthogonal and the spread is genuinely two-dimensional rather than a line.
func project(cent [][]float32) (xs, ys []float64, degenerate bool) {
	n := len(cent)
	xs, ys = make([]float64, n), make([]float64, n)
	if n == 0 {
		return xs, ys, true
	}
	if n == 1 {
		xs[0], ys[0] = 0.5, 0.5
		return xs, ys, true
	}
	d := len(cent[0])
	rows := make([][]float64, n)
	mean := make([]float64, d)
	for i, v := range cent {
		row := make([]float64, d)
		for j := range row {
			if j < len(v) {
				row[j] = float64(v[j])
			}
		}
		rows[i] = row
		for j, x := range row {
			mean[j] += x / float64(n)
		}
	}
	for _, row := range rows {
		for j := range row {
			row[j] -= mean[j]
		}
	}

	c1 := principal(rows, nil)
	xs = scores(rows, c1)
	c2 := principal(rows, c1)
	ys = scores(rows, c2)

	// Rescaling per axis is what makes the picture readable: without it a
	// dominant first component would squash everything onto a horizontal line.
	xDeg := rescale(xs)
	yDeg := rescale(ys)
	return xs, ys, xDeg && yDeg
}

// principal returns the leading eigenvector of the covariance implied by rows,
// optionally with a previously found component projected out. The covariance
// matrix is never formed — at 1536 dimensions it would be 2.4M entries — so
// each iteration multiplies through the data instead.
func principal(rows [][]float64, deflate []float64) []float64 {
	d := len(rows[0])
	v := make([]float64, d)
	// A fixed, non-uniform start makes the result reproducible. A uniform
	// vector is a poor choice: it can sit exactly on a symmetry of the data and
	// never rotate towards the true component.
	for j := range v {
		v[j] = math.Sin(float64(j)*0.7 + 1)
	}
	tmp := make([]float64, d)
	for iter := 0; iter < 64; iter++ {
		if deflate != nil {
			orthogonalize(v, deflate)
		}
		for j := range tmp {
			tmp[j] = 0
		}
		for _, row := range rows {
			var p float64
			for j, x := range row {
				p += x * v[j]
			}
			for j, x := range row {
				tmp[j] += p * x
			}
		}
		copy(v, tmp)
		if deflate != nil {
			orthogonalize(v, deflate)
		}
		norm := 0.0
		for _, x := range v {
			norm += x * x
		}
		if norm == 0 {
			return v // no variance left in this direction
		}
		inv := 1 / math.Sqrt(norm)
		for j := range v {
			v[j] *= inv
		}
	}
	return v
}

// orthogonalize removes the component of v lying along u (u is unit length).
func orthogonalize(v, u []float64) {
	var p float64
	for j := range v {
		p += v[j] * u[j]
	}
	for j := range v {
		v[j] -= p * u[j]
	}
}

func scores(rows [][]float64, axis []float64) []float64 {
	out := make([]float64, len(rows))
	for i, row := range rows {
		var s float64
		for j, x := range row {
			s += x * axis[j]
		}
		out[i] = s
	}
	return out
}

// rescale maps values into 0..1 in place, reporting whether the axis carried no
// usable spread — in which case everything is centred rather than divided by a
// vanishing range.
func rescale(v []float64) (degenerate bool) {
	if len(v) == 0 {
		return true
	}
	lo, hi := v[0], v[0]
	for _, x := range v {
		lo = math.Min(lo, x)
		hi = math.Max(hi, x)
	}
	if hi-lo < 1e-9 {
		for i := range v {
			v[i] = 0.5
		}
		return true
	}
	for i := range v {
		v[i] = (v[i] - lo) / (hi - lo)
	}
	return false
}
