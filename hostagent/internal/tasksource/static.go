package tasksource

import "context"

// Static is a deterministic, network-free Source used by tests and the demo
// mode. It also documents the contract real adapters must honour — notably the
// Since cursor filtering used for incremental pulls.
//
// Real providers (Jira / Trello / Notion) implement Source the same way but
// issue their reads through the security gateway: constructed with the gateway
// base URL + ORCHESTRA_SESSION, they call e.g. GET {gateway}/jira/... so the
// allowlist / SSRF-deny / key-injection apply and the adapter holds no token.
type Static struct {
	name    string
	tickets []Ticket
}

// NewStatic builds a Static source.
func NewStatic(name string, tickets ...Ticket) *Static {
	return &Static{name: name, tickets: tickets}
}

// Name implements Source.
func (s *Static) Name() string { return s.name }

// Fetch returns the canned tickets, applying the Since cursor and Limit so the
// incremental-pull path is exercised.
func (s *Static) Fetch(_ context.Context, q Query) ([]Ticket, error) {
	out := make([]Ticket, 0, len(s.tickets))
	for _, t := range s.tickets {
		if q.Since != "" && t.UpdatedAt <= q.Since {
			continue
		}
		out = append(out, t)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}
