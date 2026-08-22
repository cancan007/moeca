package tools

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// A generated PNG is a few megabytes, and five of them put a request past the
// gateway's body limit. The pictures were never the problem — PNG was, for a
// copy being shown to a model rather than kept.

// photoPNG builds a PNG with photographic-ish content, which is what a
// generated image actually is: PNG stores it badly.
func photoPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Smooth gradients with a little grain: what a generated picture looks like
	// to an encoder. PNG stores it badly and JPEG stores it well, which is the
	// whole reason this code exists.
	seed := uint32(12345)
	next := func() uint32 { seed = seed*1664525 + 1013904223; return seed }
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			grain := int(next()>>24) % 24
			img.Set(x, y, color.RGBA{
				uint8((x*255/w + grain) % 256),
				uint8((y*255/h + grain) % 256),
				uint8(((x+y)*255/(w+h) + grain) % 256),
				255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAPhotographicPNGTravelsAsJPEG(t *testing.T) {
	src := photoPNG(t, 1600, 1600)
	if len(src) < transcodeThreshold {
		t.Fatalf("test fixture is only %d bytes; it must exceed the threshold", len(src))
	}
	out, mt := forTransport(src, "image/png")

	if mt != "image/jpeg" {
		t.Errorf("media type = %q, want image/jpeg", mt)
	}
	if len(out) >= len(src) {
		t.Errorf("re-encoding produced %d bytes from %d — no saving", len(out), len(src))
	}
	// The point is a large saving, not a marginal one.
	if len(out) > len(src)/4 {
		t.Errorf("only shrank %d → %d; expected far better on photographic content", len(src), len(out))
	}
	// And it must still be a decodable image, or the model sees nothing.
	if _, _, err := image.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("the re-encoded image does not decode: %v", err)
	}
}

// A small image is left alone: the saving would not pay for the decode, and a
// small PNG is usually a diagram, where JPEG is the worse encoding.
func TestASmallImageIsLeftAlone(t *testing.T) {
	src := photoPNG(t, 20, 20)
	out, mt := forTransport(src, "image/png")
	if mt != "image/png" || !bytes.Equal(out, src) {
		t.Errorf("a small image was re-encoded (%s, %d → %d)", mt, len(src), len(out))
	}
}

// Anything that does not decode is passed through rather than dropped: it is
// not this code's job to decide the picture is unusable.
func TestUndecodableBytesArePassedThrough(t *testing.T) {
	src := bytes.Repeat([]byte("not an image"), 100_000)
	out, mt := forTransport(src, "image/png")
	if mt != "image/png" || !bytes.Equal(out, src) {
		t.Errorf("undecodable bytes were altered")
	}
}

// A transparent background must not come out black — that reads as a different
// picture to whoever is checking it.
func TestTransparencyIsCompositedOntoWhite(t *testing.T) {
	// Noisy opaque content — so PNG keeps it large — with a transparent hole
	// punched in the middle.
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1600))
	seed := uint32(999)
	next := func() uint32 { seed = seed*1664525 + 1013904223; return seed }
	for y := 0; y < 1600; y++ {
		for x := 0; x < 1600; x++ {
			if x > 700 && x < 900 && y > 700 && y < 900 {
				img.Set(x, y, color.RGBA{0, 0, 0, 0}) // the hole
				continue
			}
			img.Set(x, y, color.RGBA{uint8(next() >> 24), uint8(next() >> 24), uint8(next() >> 24), 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	if buf.Len() < transcodeThreshold {
		t.Fatalf("fixture is %d bytes, below the threshold", buf.Len())
	}

	out, mt := forTransport(buf.Bytes(), "image/png")
	if mt != "image/jpeg" {
		t.Fatalf("not re-encoded (%s)", mt)
	}
	got, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := got.At(800, 800).RGBA()
	if r < 0xE000 || g < 0xE000 || b < 0xE000 {
		t.Errorf("the transparent area came out as (%d,%d,%d), want white", r>>8, g>>8, b>>8)
	}
}

// The file on disk is what the run produced; only the copy in the conversation
// is re-encoded.
func TestTranscodingDoesNotTouchTheSource(t *testing.T) {
	src := photoPNG(t, 1600, 1600)
	before := append([]byte(nil), src...)
	forTransport(src, "image/png")
	if !bytes.Equal(src, before) {
		t.Error("the source bytes were modified")
	}
}
