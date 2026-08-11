package agent

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"orchestra/agent/internal/llm"
)

// logLine is one A2A-style structured log record emitted per turn/tool/event.
// It is written as a single JSON line to stdout, where `docker logs` captures
// it for the host agent to stream and inspect.
type logLine struct {
	Time       string     `json:"time"`
	Type       string     `json:"type"`
	Iteration  int        `json:"iteration,omitempty"`
	Role       string     `json:"role,omitempty"`
	StopReason string     `json:"stopReason,omitempty"`
	Tool       string     `json:"tool,omitempty"`
	ToolCalls  []string   `json:"toolCalls,omitempty"`
	IsError    bool       `json:"isError,omitempty"`
	Model      string     `json:"model,omitempty"`
	Task       string     `json:"task,omitempty"`
	Message    string     `json:"message,omitempty"`
	Usage      *llm.Usage `json:"usage,omitempty"`

	// handoff manifest (Type == "handoff"): what this stage published for the
	// stages that depend on it.
	Stage string   `json:"stage,omitempty"`
	Files []string `json:"files,omitempty"`

	// compaction accounting (Type == "compaction")
	Before int `json:"before,omitempty"` // message count before summarizing
	After  int `json:"after,omitempty"`  // message count after summarizing
	Tokens int `json:"tokens,omitempty"` // context size that triggered it
}

// logger serialises log lines to a writer. Writes are mutex-guarded so the
// loop can log from a single goroutine without interleaving output.
type logger struct {
	mu  sync.Mutex
	w   io.Writer
	now func() time.Time
}

func newLogger(w io.Writer, now func() time.Time) *logger {
	if w == nil {
		w = os.Stdout
	}
	if now == nil {
		now = time.Now
	}
	return &logger{w: w, now: now}
}

func (l *logger) event(line logLine) {
	line.Time = l.now().UTC().Format(time.RFC3339Nano)
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	b = append(b, '\n')
	l.w.Write(b)
}
