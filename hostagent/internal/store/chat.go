package store

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

// Chat conversations and their turns. See migrations/0012_chat.sql for why a
// compacted turn is marked rather than deleted.

// Turn kinds.
const (
	TurnUser    = "user"
	TurnAgent   = "agent"
	TurnSummary = "summary"
)

// ChatConversation is one standing conversation. It doubles as the JSON shape
// the /chat API returns, so the field tags must stay stable.
type ChatConversation struct {
	ID            string          `json:"id"` // "chat-<n>"
	Title         string          `json:"title"`
	TemplateRef   string          `json:"templateRef"`
	TemplateLabel string          `json:"templateLabel"`
	Scope         *KnowledgeScope `json:"scope,omitempty"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
	// TurnCount and LastText are read-only summaries for the conversation rail,
	// filled by ChatConversations (not by ChatConversation reads of one row).
	TurnCount int    `json:"turnCount"`
	LastText  string `json:"lastText"`
}

// ChatTurn is one entry of a conversation: something the user said, something
// an agent answered, or a summary standing in for a stretch of both.
type ChatTurn struct {
	ID             int64  `json:"id"`
	ConversationID string `json:"conversationId"`
	Seq            int    `json:"seq"`
	Kind           string `json:"kind"`
	Text           string `json:"text"`
	// Quotes is what this message was replying to (user turns only).
	Quotes     []string `json:"quotes,omitempty"`
	RunID      string   `json:"runId,omitempty"`
	OutputDir  string   `json:"outputDir,omitempty"`
	Status     string   `json:"status,omitempty"`
	Agent      string   `json:"agent,omitempty"`
	TokensEst  int      `json:"tokensEst"`
	Dropped    bool     `json:"dropped"`
	CoversFrom int      `json:"coversFrom,omitempty"`
	CoversTo   int      `json:"coversTo,omitempty"`
	CreatedAt  string   `json:"createdAt"`
}

// chatSeqOf parses the "chat-<n>" id back to its row key. An unparseable id
// yields 0, which matches no row.
func chatSeqOf(id string) int64 {
	n, _ := strconv.ParseInt(strings.TrimPrefix(id, "chat-"), 10, 64)
	return n
}

func chatIDOf(seq int64) string { return "chat-" + strconv.FormatInt(seq, 10) }

// CreateChat inserts a conversation and returns it with its assigned id.
func (s *SQLiteStore) CreateChat(c *ChatConversation) (*ChatConversation, error) {
	kind, id := "", ""
	if c.Scope != nil {
		kind, id = c.Scope.Kind, c.Scope.ID
	}
	res, err := s.db.Exec(
		`INSERT INTO chat_conversations (title, template_ref, template_label, scope_kind, scope_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, datetime('now'), datetime('now'))`,
		c.Title, c.TemplateRef, c.TemplateLabel, kind, id)
	if err != nil {
		return nil, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.ChatByID(chatIDOf(seq))
}

// UpdateChat rewrites a conversation's mutable fields. Returns nil when the id
// is unknown.
func (s *SQLiteStore) UpdateChat(c *ChatConversation) (*ChatConversation, error) {
	kind, id := "", ""
	if c.Scope != nil {
		kind, id = c.Scope.Kind, c.Scope.ID
	}
	res, err := s.db.Exec(
		`UPDATE chat_conversations SET title=?, template_ref=?, template_label=?, scope_kind=?, scope_id=?, updated_at=datetime('now')
		 WHERE seq=?`,
		c.Title, c.TemplateRef, c.TemplateLabel, kind, id, chatSeqOf(c.ID))
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return s.ChatByID(c.ID)
}

// DeleteChat removes a conversation and (by cascade) its turns. The bool
// reports whether a row existed. Its output directories are removed by the
// caller — the store owns rows, not directories.
func (s *SQLiteStore) DeleteChat(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM chat_conversations WHERE seq = ?`, chatSeqOf(id))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

const chatColumns = `seq, title, template_ref, template_label, scope_kind, scope_id, created_at, updated_at`

func scanChat(sc scanner) (*ChatConversation, error) {
	var seq int64
	var kind, id string
	out := &ChatConversation{}
	if err := sc.Scan(&seq, &out.Title, &out.TemplateRef, &out.TemplateLabel, &kind, &id,
		&out.CreatedAt, &out.UpdatedAt); err != nil {
		return nil, err
	}
	out.ID = chatIDOf(seq)
	if kind != "" {
		out.Scope = &KnowledgeScope{Kind: kind, ID: id}
	}
	return out, nil
}

// ChatByID returns one conversation (nil if not found).
func (s *SQLiteStore) ChatByID(id string) (*ChatConversation, error) {
	c, err := scanChat(s.db.QueryRow(
		`SELECT `+chatColumns+` FROM chat_conversations WHERE seq = ?`, chatSeqOf(id)))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// ChatConversations returns every conversation, most recently touched first,
// each carrying the turn count and the last thing said in it — what the rail
// needs, so it does not have to load every transcript to draw a list.
func (s *SQLiteStore) ChatConversations() ([]*ChatConversation, error) {
	rows, err := s.db.Query(`SELECT ` + chatColumns + ` FROM chat_conversations ORDER BY updated_at DESC, seq DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ChatConversation
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, c := range out {
		// Counted over every turn including dropped ones: the rail shows how long
		// a conversation is, which compaction does not shorten.
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM chat_turns WHERE conversation_id = ?`,
			chatSeqOf(c.ID)).Scan(&c.TurnCount)
		_ = s.db.QueryRow(
			`SELECT text FROM chat_turns WHERE conversation_id = ? AND kind != ? ORDER BY seq DESC LIMIT 1`,
			chatSeqOf(c.ID), TurnSummary).Scan(&c.LastText)
	}
	return out, nil
}

// TouchChat stamps updated_at so the rail's ordering follows activity.
func (s *SQLiteStore) TouchChat(id string) error {
	_, err := s.db.Exec(`UPDATE chat_conversations SET updated_at = datetime('now') WHERE seq = ?`, chatSeqOf(id))
	return err
}

const turnColumns = `id, conversation_id, seq, kind, text, quotes, run_id, output_dir, status, agent, tokens_est, dropped, covers_from, covers_to, created_at`

func scanTurn(sc scanner) (ChatTurn, error) {
	var t ChatTurn
	var convSeq int64
	var dropped int
	var quotes string
	err := sc.Scan(&t.ID, &convSeq, &t.Seq, &t.Kind, &t.Text, &quotes, &t.RunID, &t.OutputDir, &t.Status,
		&t.Agent, &t.TokensEst, &dropped, &t.CoversFrom, &t.CoversTo, &t.CreatedAt)
	t.ConversationID = chatIDOf(convSeq)
	t.Dropped = dropped != 0
	if quotes != "" {
		// A row whose quotes column is unreadable renders as a message with no
		// quote rather than failing the whole transcript read.
		_ = json.Unmarshal([]byte(quotes), &t.Quotes)
	}
	return t, err
}

// NextTurnSeq returns the seq a new turn should take (1 for an empty
// conversation). It is the conversation's ordering key and is never reused.
func (s *SQLiteStore) NextTurnSeq(convID string) (int, error) {
	var max sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM chat_turns WHERE conversation_id = ?`,
		chatSeqOf(convID)).Scan(&max); err != nil {
		return 0, err
	}
	return int(max.Int64) + 1, nil
}

// AddTurn appends a turn, filling in its id. The caller supplies Seq (from
// NextTurnSeq) so a user turn and the agent turn answering it are consecutive
// and both known before either runs.
func (s *SQLiteStore) AddTurn(t *ChatTurn) error {
	quotes := ""
	if len(t.Quotes) > 0 {
		if b, err := json.Marshal(t.Quotes); err == nil {
			quotes = string(b)
		}
	}
	res, err := s.db.Exec(
		`INSERT INTO chat_turns (conversation_id, seq, kind, text, quotes, run_id, output_dir, status, agent, tokens_est, dropped, covers_from, covers_to, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))`,
		chatSeqOf(t.ConversationID), t.Seq, t.Kind, t.Text, quotes, t.RunID, t.OutputDir, t.Status,
		t.Agent, t.TokensEst, b2i(t.Dropped), t.CoversFrom, t.CoversTo)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// SetTurnRun attaches the orchestrator run id to a turn. It is a separate write
// because the row has to exist before the run is submitted — a run that starts
// and is never recorded is worse than a row with no run id yet.
func (s *SQLiteStore) SetTurnRun(id int64, runID string) error {
	_, err := s.db.Exec(`UPDATE chat_turns SET run_id = ? WHERE id = ?`, runID, id)
	return err
}

// FinishTurn records how an agent turn ended and what it said.
func (s *SQLiteStore) FinishTurn(id int64, status, text string, tokensEst int) error {
	_, err := s.db.Exec(
		`UPDATE chat_turns SET status = ?, text = ?, tokens_est = ? WHERE id = ?`,
		status, text, tokensEst, id)
	return err
}

// turnOrder is how turns are read back.
//
// A summary is written after the fact, so its seq is simply the next free one —
// but its PLACE in the conversation is immediately after the range it stands
// for, which is what `covers_to` records. Ordering by a doubled key puts each
// summary in that place (`covers_to * 2 + 1` falls between `covers_to * 2` and
// the next turn's `* 2`) without renumbering anything or leaving gaps in seq to
// insert into later.
const turnOrder = `ORDER BY (CASE WHEN kind = 'summary' THEN covers_to * 2 + 1 ELSE seq * 2 END), id`

// Turns returns a conversation's turns in order, dropped ones included: the UI
// shows what a summary replaced, and re-compaction needs the original text.
func (s *SQLiteStore) Turns(convID string) ([]ChatTurn, error) {
	rows, err := s.db.Query(`SELECT `+turnColumns+` FROM chat_turns WHERE conversation_id = ? `+turnOrder,
		chatSeqOf(convID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatTurn{}
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TurnByID returns one turn (nil if not found).
func (s *SQLiteStore) TurnByID(id int64) (*ChatTurn, error) {
	t, err := scanTurn(s.db.QueryRow(`SELECT `+turnColumns+` FROM chat_turns WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// AddSummaryTurn inserts a briefing standing for the inclusive turn range
// [from, to]. Its seq is the next free one — only its uniqueness matters, since
// where it READS is decided by covers_to (see turnOrder).
func (s *SQLiteStore) AddSummaryTurn(convID, text string, from, to, tokensEst int) error {
	seq, err := s.NextTurnSeq(convID)
	if err != nil {
		return err
	}
	return s.AddTurn(&ChatTurn{
		ConversationID: convID, Seq: seq, Kind: TurnSummary, Text: text,
		TokensEst: tokensEst, CoversFrom: from, CoversTo: to,
	})
}

// MarkDropped flags the named turns as replaced by a summary. The rows keep
// their text — see the migration's note on why this is a mark and not a delete.
//
// Addressed by id rather than by a seq range, which a range cannot do correctly:
// a summary's seq is the next free one at the time it was written, NOT its place
// in the conversation, so a range spanning an earlier briefing would also span
// the turns that came after it — and those are exactly the recent ones that must
// stay verbatim. Ids say what is meant.
//
// Earlier summaries are dropped along with everything else they are folded into:
// a briefing that has been summarized again must stop being sent, or the same
// history reaches the model twice.
func (s *SQLiteStore) MarkDropped(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	q := `UPDATE chat_turns SET dropped = 1 WHERE id IN (?` + strings.Repeat(", ?", len(ids)-1) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err := s.db.Exec(q, args...)
	return err
}

// RunningTurns returns every agent turn still marked as running, across all
// conversations.
//
// A turn is finished by a goroutine that polls the run. That goroutine dies with
// the process, so an app that is quit — or replaced — while a turn is in flight
// leaves the row saying "running" with nobody left to change it, and the
// conversation reads as permanently busy. This is what the reconciler reads on
// startup to settle them.
func (s *SQLiteStore) RunningTurns() ([]ChatTurn, error) {
	rows, err := s.db.Query(`SELECT ` + turnColumns + ` FROM chat_turns WHERE kind = 'agent' AND status = 'running' ` + turnOrder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChatTurn{}
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ChatDirs returns every output directory recorded against a conversation, so a
// delete can remove the files as well as the rows.
func (s *SQLiteStore) ChatDirs(convID string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT output_dir FROM chat_turns WHERE conversation_id = ? AND output_dir != ''`, chatSeqOf(convID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// LiveTurns is a convenience for the compactor: the turns still being sent to
// the model (not dropped), oldest first.
func (s *SQLiteStore) LiveTurns(convID string) ([]ChatTurn, error) {
	all, err := s.Turns(convID)
	if err != nil {
		return nil, err
	}
	out := make([]ChatTurn, 0, len(all))
	for _, t := range all {
		if !t.Dropped {
			out = append(out, t)
		}
	}
	return out, nil
}
