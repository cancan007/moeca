package gateway

import "sync"

// bytesPerToken is a coarse estimator (~4 bytes/token) used only as a FALLBACK
// when a response reports no parseable usage (e.g. tool services, or a model
// response whose usage was truncated). Model responses are charged their real
// token usage (see extractUsage / estimateTokens).
const bytesPerToken = 4

// estimateTokens is the byte-based fallback charge (min 1 token).
func estimateTokens(reqBytes, respBytes int64) int64 {
	t := (reqBytes + respBytes) / bytesPerToken
	if t < 1 {
		t = 1
	}
	return t
}

// budgetLedger tracks estimated tokens spent per (session, service).
type budgetLedger struct {
	mu    sync.Mutex
	spent map[string]int64
}

func newBudgetLedger() *budgetLedger {
	return &budgetLedger{spent: map[string]int64{}}
}

// exceeded reports whether the key has already met/passed its ceiling.
// max <= 0 means unlimited.
func (b *budgetLedger) exceeded(key string, max int64) bool {
	if max <= 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent[key] >= max
}

// add charges tokens to the key and returns the new total (min 1 token).
func (b *budgetLedger) add(key string, tokens int64) int64 {
	if tokens < 1 {
		tokens = 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spent[key] += tokens
	return b.spent[key]
}

// snapshot returns a copy of the current ledger for status reporting.
func (b *budgetLedger) snapshot() map[string]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int64, len(b.spent))
	for k, v := range b.spent {
		out[k] = v
	}
	return out
}
