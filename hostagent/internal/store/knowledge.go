package store

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// Storage for the Knowledge graph: organizations, projects, groups, the
// membership of indexed sources in groups, and the relations between groups.
//
// The api layer maps these rows to its own shapes; they live here for the same
// reason RepoRow does, to keep the packages independent.

// KnowledgeOrg is an organization.
type KnowledgeOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// KnowledgeProject belongs to exactly one organization.
type KnowledgeProject struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	OrgID string `json:"orgId"`
}

// KnowledgeGroup is the unit retrieval permissions are expressed in. Projects
// lists the projects it serves; Sources the indexed sources it contains.
type KnowledgeGroup struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Color       string   `json:"color"`
	Owner       string   `json:"owner"`
	Description string   `json:"description"`
	Projects    []string `json:"projects"`
	Sources     []string `json:"sources"`
}

// KnowledgeRelation is a human-declared link between two groups. It is a map
// and a retrieval hint, never a grant.
type KnowledgeRelation struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// slugRe matches the characters a generated id may keep. Everything else — CJK
// included — is dropped, which is why Slug falls back to a hash when nothing
// usable survives.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug derives a stable, readable id from a display name.
//
// The id doubles as the permission tag sent to the indexer, so it must not
// change when the name is edited — callers generate it once at creation. Names
// are frequently Japanese and reduce to nothing here, so a deterministic hash
// of the original stands in rather than an empty id.
func Slug(name, prefix string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		var h uint32 = 2166136261
		for _, r := range name {
			h = (h ^ uint32(r)) * 16777619
		}
		s = fmt.Sprintf("%08x", h)
	}
	if prefix != "" {
		return prefix + "-" + s
	}
	return s
}

// uniqueID appends a numeric suffix until the id is free in the given table.
// Two groups may legitimately be named the same thing; their tags may not be,
// or one would inherit the other's permissions.
func (s *SQLiteStore) uniqueID(table, base string) (string, error) {
	id := base
	for i := 2; ; i++ {
		var n int
		// The table name is not user input — callers pass a literal — so the
		// interpolation here cannot carry injection.
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE id = ?`, id).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return id, nil
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

// --- organizations ---

func (s *SQLiteStore) KnowledgeOrgs() ([]KnowledgeOrg, error) {
	rows, err := s.db.Query(`SELECT id, name FROM knowledge_orgs ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnowledgeOrg{}
	for rows.Next() {
		var o KnowledgeOrg
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) AddKnowledgeOrg(name string) (KnowledgeOrg, error) {
	id, err := s.uniqueID("knowledge_orgs", Slug(name, "org"))
	if err != nil {
		return KnowledgeOrg{}, err
	}
	_, err = s.db.Exec(
		`INSERT INTO knowledge_orgs (id, name, created_at) VALUES (?, ?, datetime('now'))`, id, name)
	return KnowledgeOrg{ID: id, Name: name}, err
}

func (s *SQLiteStore) RenameKnowledgeOrg(id, name string) (bool, error) {
	res, err := s.db.Exec(`UPDATE knowledge_orgs SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *SQLiteStore) DeleteKnowledgeOrg(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM knowledge_orgs WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// --- projects ---

func (s *SQLiteStore) KnowledgeProjects() ([]KnowledgeProject, error) {
	rows, err := s.db.Query(`SELECT id, name, org_id FROM knowledge_projects ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnowledgeProject{}
	for rows.Next() {
		var p KnowledgeProject
		if err := rows.Scan(&p.ID, &p.Name, &p.OrgID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) AddKnowledgeProject(name, orgID string) (KnowledgeProject, error) {
	id, err := s.uniqueID("knowledge_projects", Slug(name, "prj"))
	if err != nil {
		return KnowledgeProject{}, err
	}
	_, err = s.db.Exec(
		`INSERT INTO knowledge_projects (id, name, org_id, created_at) VALUES (?, ?, ?, datetime('now'))`,
		id, name, orgID)
	return KnowledgeProject{ID: id, Name: name, OrgID: orgID}, err
}

// MoveKnowledgeProject reassigns a project to another organization. A project
// belongs to exactly one, so this replaces rather than adds.
func (s *SQLiteStore) MoveKnowledgeProject(id, orgID string) (bool, error) {
	res, err := s.db.Exec(`UPDATE knowledge_projects SET org_id = ? WHERE id = ?`, orgID, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *SQLiteStore) RenameKnowledgeProject(id, name string) (bool, error) {
	res, err := s.db.Exec(`UPDATE knowledge_projects SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *SQLiteStore) DeleteKnowledgeProject(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM knowledge_projects WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// --- groups ---

// KnowledgeGroups returns every group with its project links and source
// membership already attached. The three queries are issued once each and
// stitched in memory rather than per group, since the whole graph is read
// together on every screen load.
func (s *SQLiteStore) KnowledgeGroups() ([]KnowledgeGroup, error) {
	rows, err := s.db.Query(
		`SELECT id, name, color, owner, description FROM knowledge_groups ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnowledgeGroup{}
	idx := map[string]int{}
	for rows.Next() {
		var g KnowledgeGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Color, &g.Owner, &g.Description); err != nil {
			return nil, err
		}
		g.Projects, g.Sources = []string{}, []string{}
		idx[g.ID] = len(out)
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	collect := func(query string, assign func(i int, v string)) error {
		r, err := s.db.Query(query)
		if err != nil {
			return err
		}
		defer r.Close()
		for r.Next() {
			var gid, v string
			if err := r.Scan(&gid, &v); err != nil {
				return err
			}
			if i, ok := idx[gid]; ok {
				assign(i, v)
			}
		}
		return r.Err()
	}
	if err := collect(
		`SELECT group_id, project_id FROM knowledge_group_projects ORDER BY project_id`,
		func(i int, v string) { out[i].Projects = append(out[i].Projects, v) },
	); err != nil {
		return nil, err
	}
	if err := collect(
		`SELECT group_id, source FROM knowledge_group_members ORDER BY source`,
		func(i int, v string) { out[i].Sources = append(out[i].Sources, v) },
	); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *SQLiteStore) AddKnowledgeGroup(name, color, owner, description string) (KnowledgeGroup, error) {
	id, err := s.uniqueID("knowledge_groups", Slug(name, "grp"))
	if err != nil {
		return KnowledgeGroup{}, err
	}
	_, err = s.db.Exec(
		`INSERT INTO knowledge_groups (id, name, color, owner, description, created_at)
		 VALUES (?, ?, ?, ?, ?, datetime('now'))`, id, name, color, owner, description)
	return KnowledgeGroup{
		ID: id, Name: name, Color: color, Owner: owner, Description: description,
		Projects: []string{}, Sources: []string{},
	}, err
}

// UpdateKnowledgeGroup edits the display fields. The id — and therefore the
// permission tag — is deliberately not editable.
func (s *SQLiteStore) UpdateKnowledgeGroup(id, name, color, owner, description string) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE knowledge_groups SET name = ?, color = ?, owner = ?, description = ? WHERE id = ?`,
		name, color, owner, description, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *SQLiteStore) DeleteKnowledgeGroup(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM knowledge_groups WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SetKnowledgeGroupProjects replaces a group's project links wholesale, which
// matches how the assignment screen edits them (a checkbox grid submitted as a
// set, not as individual add/remove events).
func (s *SQLiteStore) SetKnowledgeGroupProjects(groupID string, projects []string) error {
	return s.replaceLinks(`knowledge_group_projects`, `project_id`, groupID, projects)
}

// SetKnowledgeGroupSources replaces the sources a group contains.
func (s *SQLiteStore) SetKnowledgeGroupSources(groupID string, sources []string) error {
	return s.replaceLinks(`knowledge_group_members`, `source`, groupID, sources)
}

// replaceLinks swaps a group's rows in a join table inside one transaction, so
// a failure part-way cannot leave the group with a half-applied membership —
// which for the members table would mean a half-applied permission.
func (s *SQLiteStore) replaceLinks(table, col, groupID string, values []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM `+table+` WHERE group_id = ?`, groupID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		if _, err := tx.Exec(
			`INSERT INTO `+table+` (group_id, `+col+`) VALUES (?, ?)`, groupID, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GroupsForSources maps each indexed source to the group ids containing it.
// This is what the indexer needs in order to tag chunks at build time.
func (s *SQLiteStore) GroupsForSources() (map[string][]string, error) {
	rows, err := s.db.Query(
		`SELECT source, group_id FROM knowledge_group_members ORDER BY source, group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var src, gid string
		if err := rows.Scan(&src, &gid); err != nil {
			return nil, err
		}
		out[src] = append(out[src], gid)
	}
	return out, rows.Err()
}

// --- relations ---

func (s *SQLiteStore) KnowledgeRelations() ([]KnowledgeRelation, error) {
	rows, err := s.db.Query(
		`SELECT id, from_group, to_group, type FROM knowledge_relations ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnowledgeRelation{}
	for rows.Next() {
		var r KnowledgeRelation
		if err := rows.Scan(&r.ID, &r.From, &r.To, &r.Type); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) AddKnowledgeRelation(from, to, typ string) (KnowledgeRelation, error) {
	id, err := s.uniqueID("knowledge_relations", Slug(from+"-"+typ+"-"+to, "rel"))
	if err != nil {
		return KnowledgeRelation{}, err
	}
	_, err = s.db.Exec(
		`INSERT INTO knowledge_relations (id, from_group, to_group, type, created_at)
		 VALUES (?, ?, ?, ?, datetime('now'))`, id, from, to, typ)
	if err != nil {
		return KnowledgeRelation{}, err
	}
	return KnowledgeRelation{ID: id, From: from, To: to, Type: typ}, nil
}

func (s *SQLiteStore) SetKnowledgeRelationType(id, typ string) (bool, error) {
	res, err := s.db.Exec(`UPDATE knowledge_relations SET type = ? WHERE id = ?`, typ, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *SQLiteStore) DeleteKnowledgeRelation(id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM knowledge_relations WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// KnowledgeExists reports whether a row with the id is present, so handlers can
// reject a dangling reference with a 404 instead of writing an orphan.
func (s *SQLiteStore) KnowledgeExists(table, id string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE id = ?`, id).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return n > 0, err
}
