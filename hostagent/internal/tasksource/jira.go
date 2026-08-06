package tasksource

import (
	"context"
	"encoding/json"
	"net/url"
)

// Jira pulls issues assigned to the current user from Jira Cloud (REST v3),
// routed via the gateway's "/jira/" service. The gateway injects the
// Authorization header (e.g. Basic base64(email:token)); this adapter only
// knows the API path and the response shape.
type Jira struct {
	name string
	gw   *GatewayClient
}

// NewJira builds a Jira source.
func NewJira(name string, gw *GatewayClient) *Jira { return &Jira{name: name, gw: gw} }

// Name implements Source.
func (j *Jira) Name() string { return j.name }

type jiraSearch struct {
	Issues []struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
			Updated string `json:"updated"`
			Status  struct {
				Name           string `json:"name"`
				StatusCategory struct {
					Key string `json:"key"` // new | indeterminate | done
				} `json:"statusCategory"`
			} `json:"status"`
		} `json:"fields"`
	} `json:"issues"`
}

// Fetch implements Source.
func (j *Jira) Fetch(ctx context.Context, q Query) ([]Ticket, error) {
	jql := "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	path := "/jira/rest/api/3/search?fields=summary,status,updated&maxResults=" +
		itoa(limit) + "&jql=" + url.QueryEscape(jql)
	raw, err := j.gw.Do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	var res jiraSearch
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(res.Issues))
	for _, is := range res.Issues {
		if q.Since != "" && is.Fields.Updated <= q.Since {
			continue
		}
		item, _ := json.Marshal(is)
		out = append(out, Ticket{
			ID:        "jira:" + is.Key,
			Source:    j.name,
			Title:     is.Fields.Summary,
			State:     mapCategory(is.Fields.Status.StatusCategory.Key),
			UpdatedAt: is.Fields.Updated,
			Raw:       item,
		})
	}
	return out, nil
}

// mapCategory maps a Jira status category to the normalized ticket state.
func mapCategory(key string) string {
	switch key {
	case "done":
		return "closed"
	case "indeterminate":
		return "in_progress"
	default:
		return "open"
	}
}
