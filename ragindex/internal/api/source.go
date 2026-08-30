package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"orchestra/ragindex/internal/index"
)

// Serving one source back to the caller that found it.
//
// This is the only route besides /search that the gateway forwards, and the two
// are deliberately the same shape: POST, a JSON body, and no identity anywhere
// in the URL. Authorization comes from the injected header and nothing else.
//
// Putting the caller's groups in the path was considered and rejected. The
// property this whole design rests on is that a caller never names its own
// entitlements — the gateway states them and deletes whatever arrived — so a
// group in a URL is either trusted, which is a bypass, or ignored, which is
// decoration that will one day be mistaken for a check. A source also belongs
// to several groups at once, so the pair would identify nothing the path alone
// does not, while teaching the sandbox the group ids of a graph it is not
// supposed to be able to enumerate.
//
// Bytes or text is the caller's choice because the two answer different
// questions. "Show me the reference picture" wants the file. "I read five
// chunks and need the rest of the document" wants the text, which is what was
// searched rather than a second reduction that could disagree with it.

// handleSource returns one source's text or bytes, subject to the same group
// filter as a search.
func (s *Server) handleSource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		// As is "text" (default) or "raw". Text is the default because it is
		// the harmless one: it can only return what a search could already have
		// returned in pieces.
		As string `json:"as"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		writeErr(w, 400, "source is required — use the `source` field of a search result")
		return
	}
	filter := groupFilter(r)

	if strings.EqualFold(strings.TrimSpace(req.As), "raw") {
		b, media, err := s.idx.SourceBytes(req.Source, filter)
		if err != nil {
			writeSourceErr(w, err)
			return
		}
		// The bytes are handed back as an opaque stream. Naming a specific
		// image or video type here would be guessing from an extension, and the
		// caller is writing the response to a file whose name it already chose.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Orchestra-Media", media)
		w.WriteHeader(200)
		w.Write(b)
		return
	}

	text, err := s.idx.SourceText(req.Source, filter)
	if err != nil {
		writeSourceErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"source": req.Source, "text": text})
}

// writeSourceErr maps a lookup failure onto a status.
//
// Missing and forbidden are one answer, 404, and that is the point: a caller
// that could tell them apart could enumerate the sources outside its scope one
// name at a time, which is the whole thing the group filter exists to prevent.
func writeSourceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, index.ErrSourceNotAvailable):
		writeErr(w, 404, err.Error())
	case errors.Is(err, index.ErrNoBytes):
		writeErr(w, 409, err.Error())
	default:
		writeErr(w, 500, err.Error())
	}
}
