import { useMemo, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  knowledge,
  shortLabel,
  type GraphNode,
  type IndexGraph,
  type KnowledgeGraph,
} from "@/lib/knowledge";
import {
  Button,
  EmptyState,
  Notice,
  Section,
  Swatch,
  TextInput,
  monoMeta,
  panel,
  pillStyle,
  railHeading,
  selectableRow,
} from "./ui";

// Mode 3 — assignment.
//
// Three columns for three questions that are easy to conflate: which
// organizations exist, which projects belong to them, and which groups serve a
// project. The last column is also where a group's sources are chosen, which
// is the screen's only real permission edit — a source in a group is a source
// that group's tasks can retrieve.

export function AssignMode({
  graph,
  index,
  onChanged,
}: {
  graph: KnowledgeGraph;
  index: IndexGraph;
  onChanged: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [selOrg, setSelOrg] = useState<string | null>(graph.orgs[0]?.id ?? null);
  const [selProject, setSelProject] = useState<string | null>(null);
  const [selGroup, setSelGroup] = useState<string | null>(null);
  const [newOrg, setNewOrg] = useState("");
  const [newProject, setNewProject] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const org = graph.orgs.find((o) => o.id === selOrg) ?? graph.orgs[0];
  const projects = useMemo(
    () => graph.projects.filter((p) => !org || p.orgId === org.id),
    [graph.projects, org],
  );
  const project = projects.find((p) => p.id === selProject) ?? projects[0];
  const group = graph.groups.find((g) => g.id === selGroup);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError("");
    try {
      await fn();
      await onChanged();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ flex: 1, display: "flex", minHeight: 0, minWidth: 0 }}>
      <div style={{ ...panel, width: 230, flex: "none", borderRight: "1px solid var(--bd)" }}>
        <div style={{ padding: "12px 13px 9px", display: "flex", alignItems: "center", gap: 7 }}>
          <Swatch color="var(--purple)" size={7} />
          <span style={railHeading}>ORGANIZATIONS</span>
          <div style={{ flex: 1 }} />
          <span style={monoMeta}>{graph.orgs.length}</span>
        </div>
        <div style={{ padding: "0 9px 10px", display: "flex", flexDirection: "column", gap: 3 }}>
          {graph.orgs.map((o) => (
            <div
              key={o.id}
              onClick={() => {
                setSelOrg(o.id);
                setSelProject(null);
              }}
              style={selectableRow(org?.id === o.id)}
            >
              <span
                style={{
                  font: "500 11.5px 'IBM Plex Sans'",
                  color: "var(--tx3)",
                  flex: 1,
                  minWidth: 0,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {o.name}
              </span>
              <span style={monoMeta}>
                {graph.projects.filter((p) => p.orgId === o.id).length}
              </span>
            </div>
          ))}
        </div>
        <div
          style={{
            padding: "10px 12px 13px",
            margin: "0 9px 12px",
            borderTop: "1px solid var(--bd-soft)",
            display: "flex",
            flexDirection: "column",
            gap: 7,
          }}
        >
          <span style={{ ...railHeading, fontSize: 9 }}>{t("knowledge.assign.newOrg")}</span>
          <div style={{ display: "flex", gap: 6 }}>
            <TextInput
              value={newOrg}
              onChange={setNewOrg}
              placeholder={t("knowledge.assign.orgPlaceholder")}
              onEnter={() => {
                if (!newOrg.trim()) return;
                run(() => knowledge.addOrg(newOrg.trim()));
                setNewOrg("");
              }}
            />
            <Button
              tone="accent"
              disabled={busy || !newOrg.trim()}
              onClick={() => {
                run(() => knowledge.addOrg(newOrg.trim()));
                setNewOrg("");
              }}
            >
              {t("common.add")}
            </Button>
          </div>
        </div>
        {org ? (
          <div style={{ marginTop: "auto", padding: "11px 13px", borderTop: "1px solid var(--bd-soft)" }}>
            <Button
              tone="danger"
              disabled={busy}
              onClick={() => {
                run(() => knowledge.deleteOrg(org.id));
                setSelOrg(null);
              }}
            >
              {t("knowledge.assign.deleteOrg", { name: org.name })}
            </Button>
            <span
              style={{
                display: "block",
                marginTop: 7,
                font: "400 9.5px 'IBM Plex Sans'",
                color: "var(--tx-dim)",
                lineHeight: 1.6,
              }}
            >
              {t("knowledge.assign.deleteOrgNote")}
            </span>
          </div>
        ) : null}
      </div>

      <div
        style={{
          ...panel,
          width: 300,
          flex: "none",
          borderRight: "1px solid var(--bd)",
          background: "var(--bg-app)",
        }}
      >
        <div style={{ padding: "12px 14px 9px", display: "flex", alignItems: "center", gap: 7 }}>
          <Swatch color="var(--ac)" size={7} />
          <span style={railHeading}>PROJECTS</span>
          <div style={{ flex: 1 }} />
          <span style={monoMeta}>{projects.length}</span>
        </div>
        {!org ? (
          <EmptyState
            title={t("knowledge.assign.noOrgs")}
            hint={t("knowledge.assign.noOrgsHint")}
          />
        ) : (
          <>
            <div style={{ padding: "0 11px 10px", display: "flex", flexDirection: "column", gap: 4 }}>
              {projects.map((p) => (
                <div
                  key={p.id}
                  onClick={() => setSelProject(p.id)}
                  style={selectableRow(project?.id === p.id)}
                >
                  <span
                    style={{
                      font: "500 11.5px 'IBM Plex Sans'",
                      color: "var(--tx3)",
                      flex: 1,
                      minWidth: 0,
                      overflow: "hidden",
                      textOverflow: "ellipsis",
                      whiteSpace: "nowrap",
                    }}
                  >
                    {p.name}
                  </span>
                  <span style={monoMeta}>
                    {graph.groups.filter((g) => g.projects.includes(p.id)).length}
                  </span>
                </div>
              ))}
            </div>
            <div
              style={{
                padding: "10px 12px 13px",
                margin: "0 11px",
                borderTop: "1px solid var(--bd-soft)",
                display: "flex",
                flexDirection: "column",
                gap: 7,
              }}
            >
              <span style={{ ...railHeading, fontSize: 9 }}>{t("knowledge.assign.newProject")}</span>
              <div style={{ display: "flex", gap: 6 }}>
                <TextInput
                  value={newProject}
                  onChange={setNewProject}
                  placeholder={t("knowledge.assign.projectPlaceholder")}
                  onEnter={() => {
                    if (!newProject.trim()) return;
                    run(() => knowledge.addProject(newProject.trim(), org.id));
                    setNewProject("");
                  }}
                />
                <Button
                  tone="accent"
                  disabled={busy || !newProject.trim()}
                  onClick={() => {
                    run(() => knowledge.addProject(newProject.trim(), org.id));
                    setNewProject("");
                  }}
                >
                  {t("common.add")}
                </Button>
              </div>
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                {t("knowledge.assign.addedTo", { name: org.name })}
              </span>
            </div>
            {project ? (
              <Section title={t("knowledge.assign.groupsUsingProject")} meta={project.name}>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                  {graph.groups.map((g) => {
                    const on = g.projects.includes(project.id);
                    return (
                      <div
                        key={g.id}
                        onClick={
                          busy
                            ? undefined
                            : () => {
                                const next = on
                                  ? g.projects.filter((x) => x !== project.id)
                                  : [...g.projects, project.id];
                                run(() => knowledge.setLinks(g.id, { projects: next }));
                              }
                        }
                        style={pillStyle(on, g.color || "var(--ac)")}
                      >
                        {g.name}
                      </div>
                    );
                  })}
                </div>
                <Button
                  tone="danger"
                  disabled={busy}
                  onClick={() => {
                    run(() => knowledge.deleteProject(project.id));
                    setSelProject(null);
                  }}
                >
                  {t("knowledge.assign.deleteProject")}
                </Button>
              </Section>
            ) : null}
          </>
        )}
      </div>

      <div style={{ ...panel, flex: 1, minWidth: 0 }}>
        {error ? (
          <div style={{ padding: "12px 15px" }}>
            <Notice tone="error">{error}</Notice>
          </div>
        ) : null}
        <div
          style={{
            padding: "13px 15px 12px",
            borderBottom: "1px solid var(--bd-soft)",
            display: "flex",
            alignItems: "center",
            gap: 8,
          }}
        >
          <span style={railHeading}>{t("knowledge.assign.groupNodes")}</span>
          <div style={{ flex: 1 }} />
          <span style={monoMeta}>{t("knowledge.assign.indexedCount", { count: index.nodes.length })}</span>
        </div>
        <div style={{ padding: "10px 15px", display: "flex", flexWrap: "wrap", gap: 6 }}>
          {graph.groups.map((g) => (
            <div
              key={g.id}
              onClick={() => setSelGroup(g.id)}
              style={pillStyle(group?.id === g.id, g.color || "var(--ac)")}
            >
              {g.name}
            </div>
          ))}
        </div>
        {!group ? (
          <EmptyState
            title={t("knowledge.meaning.pickGroup")}
            hint={t("knowledge.assign.pickGroupHint")}
          />
        ) : index.nodes.length === 0 ? (
          <EmptyState
            title={t("knowledge.assign.noIndexedNodes")}
            hint={t("knowledge.assign.noIndexedNodesHint")}
          />
        ) : (
          <div style={{ padding: "6px 15px 18px", display: "flex", flexDirection: "column", gap: 8 }}>
            <Notice tone="info">
              <Trans i18nKey="knowledge.assign.scopeNote" values={{ name: group.name }} components={{ b: <b /> }} />
            </Notice>
            {sectionsOf(index.nodes, t("knowledge.assign.externalSection")).map(({ origin, nodes }) => (
            <div key={origin} style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              {/* The registered folder this came from. A local path is relative
                  to its root, so "README.md" alone does not say which of four
                  folders it is — and ticking the wrong one grants the wrong
                  file. The heading is where that is answered. */}
              <div style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 4 }}>
                <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>
                  {origin}
                </span>
                <div style={{ flex: 1, height: 1, background: "var(--bd-soft)" }} />
                <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>
                  {nodes.filter((n) => group.sources.includes(n.source)).length}/{nodes.length}
                </span>
              </div>
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 5 }}>
              {nodes.map((n) => {
                const on = group.sources.includes(n.source);
                return (
                  <div
                    key={n.source}
                    onClick={
                      busy
                        ? undefined
                        : () => {
                            const next = on
                              ? group.sources.filter((x) => x !== n.source)
                              : [...group.sources, n.source];
                            // Sources only: omitting projects leaves the
                            // group's project links untouched.
                            run(() => knowledge.setLinks(group.id, { sources: next }));
                          }
                    }
                    style={{
                      ...selectableRow(on),
                      background: on ? "var(--tint-active)" : "var(--bg-card)",
                      border: `1px solid ${on ? "var(--tint-active-bd)" : "var(--bd2)"}`,
                    }}
                  >
                    <div
                      style={{
                        width: 13,
                        height: 13,
                        borderRadius: 3.5,
                        flex: "none",
                        border: `1px solid ${on ? group.color || "var(--ac)" : "var(--bd2)"}`,
                        background: on ? group.color || "var(--ac)" : "transparent",
                        color: "var(--bg-deep)",
                        font: "700 9px 'IBM Plex Sans'",
                        display: "flex",
                        alignItems: "center",
                        justifyContent: "center",
                      }}
                    >
                      {on ? "✓" : ""}
                    </div>
                    <span
                      style={{
                        font: "500 10px 'IBM Plex Mono'",
                        color: on ? "var(--tx2)" : "var(--tx3)",
                        flex: 1,
                        minWidth: 0,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                      title={n.source}
                    >
                      {n.rel || shortLabel(n.source)}
                    </span>
                    <span style={{ font: "500 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                      {n.chunks}
                    </span>
                  </div>
                );
              })}
            </div>
            </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

/** Groups the index's nodes by the registered reference they came from.
 *
 *  Local folders keep their own heading and their own order; every external
 *  document lands in one section, because a URL already says where it is from
 *  and one heading per document would be a list of headings.
 *
 *  A node from an indexer too old to report an origin has none, and falls in
 *  last under the external heading rather than inventing a folder for it. */
function sectionsOf(nodes: GraphNode[], externalLabel: string): { origin: string; nodes: GraphNode[] }[] {
  const byOrigin = new Map<string, GraphNode[]>();
  for (const n of nodes) {
    const key = n.kind === "external" || !n.origin ? externalLabel : n.origin;
    byOrigin.set(key, [...(byOrigin.get(key) ?? []), n]);
  }
  return [...byOrigin.entries()]
    .map(([origin, ns]) => ({ origin, nodes: ns }))
    // Local folders first, alphabetically; the external bucket last, since it
    // is a catch-all rather than a place.
    .sort((a, b) =>
      a.origin === externalLabel ? 1 : b.origin === externalLabel ? -1 : a.origin.localeCompare(b.origin),
    );
}
