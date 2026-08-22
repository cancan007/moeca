// Shrinking an image for the journey, not on disk.
//
// A generated PNG is a few megabytes; five of them in one conversation put the
// request past the gateway's body limit and killed a run that had already done
// its work. The pictures were not the problem — PNG was. It is lossless, which
// is the right choice for a file being kept and the wrong one for a picture
// being shown to a model, which sees a downscaled raster either way.
//
// So the copy that travels is re-encoded, and the artifact on disk is left
// exactly as produced. An agent judging "is this the dog I asked for" loses
// nothing to JPEG; a run that cannot send its images loses everything.
package tools

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/gif" // registered so a GIF can be decoded for re-encoding
	"image/jpeg"
	_ "image/png" // ditto
)

// jpegQuality is high enough that nothing a reviewer would judge on survives
// differently, and low enough to turn megabytes into hundreds of kilobytes.
const jpegQuality = 82

// transcodeThreshold leaves small images alone. Below this the saving is not
// worth decoding for, and a small PNG is often a diagram, where JPEG is the
// worse encoding.
const transcodeThreshold = 512 << 10

// forTransport returns the bytes and media type to send to the model.
//
// It returns the original unchanged whenever re-encoding would not clearly win:
// a small image, an undecodable one, or a picture JPEG happens to encode no
// better. A failure here is never fatal — the caller keeps what it had.
func forTransport(b []byte, mediaType string) ([]byte, string) {
	if len(b) < transcodeThreshold || mediaType == "image/jpeg" {
		return b, mediaType
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return b, mediaType // not ours to fix; send it as it is
	}
	// Composited onto white rather than encoded directly: JPEG has no alpha, and
	// a transparent background would otherwise come out black — which reads as a
	// different picture to whoever is checking it.
	bounds := img.Bounds()
	flat := image.NewRGBA(bounds)
	draw.Draw(flat, bounds, image.NewUniform(image.White.C), image.Point{}, draw.Src)
	draw.Draw(flat, bounds, img, bounds.Min, draw.Over)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, flat, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return b, mediaType
	}
	if out.Len() >= len(b) {
		return b, mediaType // the original was already the better encoding
	}
	return out.Bytes(), "image/jpeg"
}
