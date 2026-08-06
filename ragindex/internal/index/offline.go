// Offline embeddings — a demo and test path that needs no key and no network.
//
// The indexer's real embeddings come from a model through the gateway, which
// means the Knowledge screen shows nothing at all until someone has configured
// an embeddings provider. That is the correct default for a running install and
// a bad one for a first look at the app: the screen that explains the whole
// idea is the one that cannot be reached without a key.
//
// So this computes a deterministic vector from the text itself: hashed token
// counts, L2-normalised. Documents that share vocabulary land near each other,
// which is enough for the graph to have real structure and for a search to
// return something defensible. It is NOT a semantic embedding — it cannot see
// that 冪等 and idempotent mean the same thing, and it never will. Anything
// beyond looking at the app should use a real model.
//
// Because the difference is invisible in the UI, the mode is carried in
// /status and the panel says so on screen. An index that quietly answers with
// worse vectors than the user thinks is exactly the failure this app exists to
// argue against.
package index

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// EmbedModeOffline computes vectors locally instead of calling the gateway.
const (
	EmbedModeGateway = "gateway"
	EmbedModeOffline = "offline"
)

// offlineDims is the vector width. Small enough that 50k chunks stay cheap to
// scan, wide enough that hash collisions do not dominate a few thousand tokens.
const offlineDims = 384

// offlineEmbed hashes each text into a fixed-width vector.
//
// Tokens are ASCII word runs plus character bigrams for everything else —
// Japanese has no spaces, so word tokens alone would reduce most of this
// corpus to nothing. Counts are damped with log1p so one word repeated forty
// times does not swamp the rest of the document, and each token's contribution
// is signed by a second hash bit, which keeps unrelated collisions cancelling
// instead of accumulating.
func offlineEmbed(texts []string) [][]float32 {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		counts := map[uint32]float64{}
		for _, tok := range offlineTokens(t) {
			h := fnv.New32a()
			h.Write([]byte(tok))
			counts[h.Sum32()]++
		}
		v := make([]float32, offlineDims)
		for h, n := range counts {
			w := math.Log1p(n)
			if h&0x8000 != 0 {
				w = -w
			}
			v[h%offlineDims] += float32(w)
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if norm > 0 {
			norm = math.Sqrt(norm)
			for k := range v {
				v[k] = float32(float64(v[k]) / norm)
			}
		}
		out[i] = v
	}
	return out
}

// offlineTokens splits text into hashable units: lowercased ASCII/latin word
// runs, and character bigrams over runs of everything else.
func offlineTokens(s string) []string {
	var toks []string
	var latin strings.Builder
	var other []rune

	flushLatin := func() {
		if latin.Len() > 1 { // single letters carry no signal
			toks = append(toks, latin.String())
		}
		latin.Reset()
	}
	flushOther := func() {
		for i := 0; i+1 < len(other); i++ {
			toks = append(toks, string(other[i:i+2]))
		}
		if len(other) == 1 {
			toks = append(toks, string(other))
		}
		other = other[:0]
	}

	for _, r := range strings.ToLower(s) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			flushOther()
			latin.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r): // CJK, kana, accented latin
			flushLatin()
			other = append(other, r)
		default:
			flushLatin()
			flushOther()
		}
	}
	flushLatin()
	flushOther()
	return toks
}
