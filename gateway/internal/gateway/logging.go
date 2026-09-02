package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// accessLog is one structured request/response record. It feeds the app's Audit
// view (A2A-style) and doubles as the request/response logging role.
type accessLog struct {
	Time      string `json:"time"`
	RequestID string `json:"requestId"`
	Session   string `json:"session"`
	Run       string `json:"run,omitempty"`   // ORCHESTRA run id (attribution)
	Stage     string `json:"stage,omitempty"` // ORCHESTRA stage id (attribution)
	Service   string `json:"service"`
	// Groups is the retrieval entitlement this request carried, for services
	// that enforce one. It is what the gateway INJECTED, not what the caller
	// sent — the caller's own value is discarded before this is decided.
	//
	// Recorded because the log could otherwise say what a run reached but never
	// what it was allowed to reach, and those are different questions. Answering
	// "did this run stay in scope" from the second alone means reconstructing the
	// grant from the graph as it is now, which is not the graph as it was then.
	//
	// nil for every other service, and for a session that stated no entitlement —
	// which is a state those services refuse, so it appears only on the refusal.
	Groups       []string `json:"groups,omitempty"`
	Model        string   `json:"model,omitempty"`
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	Upstream     string   `json:"upstream,omitempty"`
	Status       int      `json:"status"`
	ReqBytes     int64    `json:"reqBytes"`
	RespBytes    int64    `json:"respBytes"`
	ReqBody      string   `json:"reqBody,omitempty"`  // captured request content (capped)
	RespBody     string   `json:"respBody,omitempty"` // captured response content (capped)
	DurationMs   int64    `json:"durationMs"`
	TokensEst    int64    `json:"tokensEst,omitempty"`    // tokens charged to the budget (real usage or estimate)
	InputTokens  int      `json:"inputTokens,omitempty"`  // real prompt tokens (model services)
	OutputTokens int      `json:"outputTokens,omitempty"` // real completion tokens (model services)
	Err          string   `json:"err,omitempty"`
}

// logger writes access records as JSON lines and retains the most recent ones
// in an in-memory ring buffer for the Audit HTTP surface. Safe for concurrent use.
type logger struct {
	mu    sync.Mutex
	w     io.Writer
	ring  *ring
	store *AuditStore // optional durable, tamper-evident sink
}

func newLogger(w io.Writer) *logger { return &logger{w: w, ring: newRing(500)} }

func (l *logger) write(rec accessLog) {
	rec.Time = time.Now().UTC().Format(time.RFC3339Nano)
	l.ring.add(rec)
	if l.store != nil {
		// durable append extends the hash chain; a failure here must not drop
		// the request, so it is best-effort (the ring still has the record).
		_ = l.store.append(rec)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.w.Write(append(b, '\n'))
}

// ring is a fixed-capacity, thread-safe buffer of the most recent access records.
type ring struct {
	mu  sync.Mutex
	buf []accessLog
	cap int
}

func newRing(capacity int) *ring { return &ring{cap: capacity} }

// add appends a record, dropping the oldest once capacity is exceeded.
func (r *ring) add(rec accessLog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, rec)
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
}

// snapshot returns a copy of the retained records, newest first.
func (r *ring) snapshot() []accessLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]accessLog, len(r.buf))
	for i, rec := range r.buf {
		out[len(r.buf)-1-i] = rec
	}
	return out
}

// maxCaptureBytes caps how much request/response content is retained per record.
// Sized to hold a full multi-block response (thinking + tool_use + text) so the
// Audit view can reconstruct the agent's interpretation/output, not just a
// truncated prefix. Bounded to keep the in-memory ring buffer modest.
const maxCaptureBytes = 32 << 10

// maxUsageTail is how much of the response TAIL is retained for token-usage
// parsing. Provider usage (input/output token counts, or the terminal SSE usage
// frame) sits at the END of the response, past the 8 KiB content prefix, so a
// separate rolling tail is kept when the service is an LLM (trackUsage).
const maxUsageTail = 64 << 10

// recorder wraps http.ResponseWriter to capture status and byte count while
// preserving streaming semantics (SSE) by forwarding Flush. When capture is on
// it also tees up to maxCaptureBytes of the response body for the monitoring
// plane, and (for model services) a rolling tail for real token-usage parsing.
type recorder struct {
	http.ResponseWriter
	status     int
	bytes      int64
	wrote      bool
	capture    bool
	body       []byte
	trackUsage bool
	usageTail  []byte
}

func (r *recorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	if r.capture && len(r.body) < maxCaptureBytes {
		room := maxCaptureBytes - len(r.body)
		if room > n {
			room = n
		}
		r.body = append(r.body, p[:room]...)
	}
	if r.trackUsage {
		r.usageTail = appendTail(r.usageTail, p[:n], maxUsageTail)
	}
	return n, err
}

// appendTail appends p to buf and keeps only the last cap bytes, so a large
// (possibly streamed) response is bounded while retaining its end, where usage
// lives.
func appendTail(buf, p []byte, cap int) []byte {
	buf = append(buf, p...)
	if len(buf) > cap {
		buf = append([]byte(nil), buf[len(buf)-cap:]...)
	}
	return buf
}

// Flush forwards to the underlying writer so token-by-token streaming reaches
// the client immediately.
func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
