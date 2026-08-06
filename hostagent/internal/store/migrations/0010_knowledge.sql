-- The Knowledge graph: how retrievable knowledge is organised and who may see
-- it.
--
-- organization 1:N project, and project N:M group. A group is the unit the
-- retrieval permission check works in: a task carries group ids, and only
-- sources belonging to those groups are searchable. Projects exist to bundle
-- groups for assignment; they are not themselves a permission boundary.
--
-- Ids are slugs rather than surrogate integers because a group id travels to
-- the indexer as its permission tag. Display names can therefore be renamed
-- without silently revoking access, and a gateway config or a log line stays
-- readable.

CREATE TABLE knowledge_orgs (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE knowledge_projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    -- A project belongs to exactly one organization. ON DELETE CASCADE: an
    -- organization that is gone cannot own anything, and leaving orphans would
    -- make the assignment screen show projects under no heading.
    org_id     TEXT NOT NULL REFERENCES knowledge_orgs(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_knowledge_projects_org ON knowledge_projects(org_id);

CREATE TABLE knowledge_groups (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    color       TEXT NOT NULL DEFAULT '',
    owner       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);

-- A group may serve several projects: "セキュリティ関連" is read by every team.
CREATE TABLE knowledge_group_projects (
    group_id   TEXT NOT NULL REFERENCES knowledge_groups(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES knowledge_projects(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, project_id)
);

CREATE INDEX idx_knowledge_group_projects_project ON knowledge_group_projects(project_id);

-- Which indexed sources a group contains. The source is identified by the same
-- path/URL the indexer reports, so membership survives a reindex; a source that
-- disappears simply stops matching and its row is harmless until cleaned up.
CREATE TABLE knowledge_group_members (
    group_id TEXT NOT NULL REFERENCES knowledge_groups(id) ON DELETE CASCADE,
    source   TEXT NOT NULL,
    PRIMARY KEY (group_id, source)
);

CREATE INDEX idx_knowledge_group_members_source ON knowledge_group_members(source);

-- Human-declared relations between groups. These are a map and a retrieval
-- hint; they never grant access, so traversal must still be filtered by the
-- caller's own groups.
--
-- Direction is recorded because it carries meaning for a reader ("A supersedes
-- B"), but retrieval traverses both ways.
CREATE TABLE knowledge_relations (
    id         TEXT PRIMARY KEY,
    from_group TEXT NOT NULL REFERENCES knowledge_groups(id) ON DELETE CASCADE,
    to_group   TEXT NOT NULL REFERENCES knowledge_groups(id) ON DELETE CASCADE,
    type       TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_knowledge_relations_from ON knowledge_relations(from_group);
CREATE INDEX idx_knowledge_relations_to ON knowledge_relations(to_group);
