package tasksource

import (
	"context"
	"encoding/json"
)

// Trello pulls the current member's cards, routed via the gateway's "/trello/"
// service. The gateway injects the Authorization header (the OAuth key/token
// form); this adapter only knows the API path and response shape.
type Trello struct {
	name string
	gw   *GatewayClient
}

// NewTrello builds a Trello source.
func NewTrello(name string, gw *GatewayClient) *Trello { return &Trello{name: name, gw: gw} }

// Name implements Source.
func (t *Trello) Name() string { return t.name }

type trelloCard struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	URL              string `json:"url"`
	DateLastActivity string `json:"dateLastActivity"`
	Closed           bool   `json:"closed"`
}

// Fetch implements Source.
func (t *Trello) Fetch(ctx context.Context, q Query) ([]Ticket, error) {
	path := "/trello/1/members/me/cards?fields=name,url,dateLastActivity,closed"
	raw, err := t.gw.Do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var cards []trelloCard
	if err := json.Unmarshal(raw, &cards); err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(cards))
	for _, c := range cards {
		if q.Since != "" && c.DateLastActivity <= q.Since {
			continue
		}
		state := "open"
		if c.Closed {
			state = "closed"
		}
		item, _ := json.Marshal(c)
		out = append(out, Ticket{
			ID:        "trello:" + c.ID,
			Source:    t.name,
			Title:     c.Name,
			URL:       c.URL,
			State:     state,
			UpdatedAt: c.DateLastActivity,
			Raw:       item,
		})
	}
	return out, nil
}
