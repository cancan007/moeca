package tasksource

import (
	"context"
	"encoding/json"
)

// Notion pulls pages the integration can see, routed via the gateway's
// "/notion/" service. The gateway injects the Authorization + Notion-Version
// headers; this adapter only knows the API path and response shape.
type Notion struct {
	name string
	gw   *GatewayClient
}

// NewNotion builds a Notion source.
func NewNotion(name string, gw *GatewayClient) *Notion { return &Notion{name: name, gw: gw} }

// Name implements Source.
func (n *Notion) Name() string { return n.name }

type notionSearch struct {
	Results []struct {
		ID             string                    `json:"id"`
		URL            string                    `json:"url"`
		LastEditedTime string                    `json:"last_edited_time"`
		Properties     map[string]notionProperty `json:"properties"`
	} `json:"results"`
}

type notionProperty struct {
	Type  string           `json:"type"`
	Title []notionRichText `json:"title"`
}

type notionRichText struct {
	PlainText string `json:"plain_text"`
}

// Fetch implements Source.
func (n *Notion) Fetch(ctx context.Context, q Query) ([]Ticket, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	body, _ := json.Marshal(map[string]any{
		"filter":    map[string]string{"value": "page", "property": "object"},
		"sort":      map[string]string{"direction": "descending", "timestamp": "last_edited_time"},
		"page_size": limit,
	})
	raw, err := n.gw.Do(ctx, "POST", "/notion/v1/search", body)
	if err != nil {
		return nil, err
	}
	var res notionSearch
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(res.Results))
	for _, p := range res.Results {
		if q.Since != "" && p.LastEditedTime <= q.Since {
			continue
		}
		item, _ := json.Marshal(p)
		out = append(out, Ticket{
			ID:        "notion:" + p.ID,
			Source:    n.name,
			Title:     notionTitle(p.Properties),
			URL:       p.URL,
			State:     "open",
			UpdatedAt: p.LastEditedTime,
			Raw:       item,
		})
	}
	return out, nil
}

// notionTitle extracts the first title-typed property's plain text.
func notionTitle(props map[string]notionProperty) string {
	for _, p := range props {
		if p.Type == "title" && len(p.Title) > 0 {
			return p.Title[0].PlainText
		}
	}
	return "(untitled)"
}
