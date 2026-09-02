import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  GROUP_COLORS,
  RELATION_MEANING,
  RELATION_STYLE,
  RELATION_TYPES,
  knowledge,
  shortLabel,
  type IndexGraph,
  type KnowledgeGraph,
  type KnowledgeGroup,
  type RelationType,
} from "@/lib/knowledge";
import { centroid, curveBetween, hullPath, type Point } from "./geometry";
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

// Mode 2 — the declared view.
//
// Where mode 1 shows what the embeddings say, this shows what a person decided:
// groups drawn as regions over the same node positions, and arrows between
// them. Reusing the projection for both is deliberate — laying regions out
// separately would make the two modes incomparable, and comparing them is the
// reason for having both.

const W = 700;
const H = 520;
const PAD = 34;

export function MeaningMode({
  graph,
  index,
  onChanged,
}: {
  graph: KnowledgeGraph;
  index: IndexGraph;
  onChanged: () => Promise<void>;
}) {
  const { t } = useTranslation();
  const [focus, setFocus] = useState<string | null>(graph.groups[0]?.id ?? null);
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const focused = graph.groups.find((g) => g.id === focus) ?? graph.groups[0];

  const pos = useMemo(() => {
    const m = new Map<string, Point>();
    for (const n of index.nodes) {
      m.set(n.source, [PAD + n.x * (W - PAD * 2), PAD + n.y * (H - PAD * 2)]);
    }
    return m;
  }, [index.nodes]);

  const pointsOf = (g: KnowledgeGroup): Point[] =>
    g.sources.map((s) => pos.get(s)).filter((p): p is Point => !!p);

  // Only groups whose members are actually in the index can be drawn. A group
  // listing sources that no longer exist is legitimate — the file may come
  // back — so it stays in the rail and simply has no outline.
  const drawable = useMemo(
    () => graph.groups.map((g) => ({ g, pts: pointsOf(g) })).filter(({ pts }) => pts.length > 0),
    [graph.groups, pos],
  );

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

  const relationsOf = (id: string) => graph.relations.filter((r) => r.from === id || r.to === id);

  return (
    <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
      <div style={{ ...panel, width: 212, flex: "none", borderRight: "1px solid var(--bd)" }}>
        <div style={{ padding: "12px 13px", display: "flex", flexDirection: "column", gap: 6 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 7, marginBottom: 2 }}>
            <span style={railHeading}>GROUPS</span>
            <div style={{ flex: 1 }} />
            <span style={monoMeta}>{graph.groups.length}</span>
          </div>
          {graph.groups.map((g) => (
            <div key={g.id} onClick={() => setFocus(g.id)} style={selectableRow(focus === g.id)}>
              <Swatch color={g.color} />
              <span
                style={{
                  font: "500 11px 'IBM Plex Sans'",
                  flex: 1,
                  minWidth: 0,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  color: "var(--tx3)",
                }}
              >
                {g.name}
              </span>
              <span style={monoMeta}>{g.sources.length}</span>
            </div>
          ))}
        </div>
        <div
          style={{
            padding: "10px 13px 13px",
            margin: "0 9px",
            borderTop: "1px solid var(--bd-soft)",
            display: "flex",
            flexDirection: "column",
            gap: 7,
          }}
        >
          <span style={{ ...railHeading, fontSize: 9 }}>{t("knowledge.meaning.newGroup")}</span>
          <div style={{ display: "flex", gap: 6 }}>
            <TextInput
              value={newName}
              onChange={setNewName}
              placeholder={t("knowledge.meaning.groupPlaceholder")}
              onEnter={() => {
                if (!newName.trim()) return;
                const color = GROUP_COLORS[graph.groups.length % GROUP_COLORS.length];
                run(() => knowledge.addGroup(newName.trim(), color));
                setNewName("");
              }}
            />
            <Button
              tone="accent"
              disabled={busy || !newName.trim()}
              onClick={() => {
                const color = GROUP_COLORS[graph.groups.length % GROUP_COLORS.length];
                run(() => knowledge.addGroup(newName.trim(), color));
                setNewName("");
              }}
            >
              {t("common.add")}
            </Button>
          </div>
        </div>
      </div>

      <div
        style={{
          flex: 1,
          position: "relative",
          minWidth: 0,
          background: "var(--bg-app)",
          overflow: "hidden",
        }}
      >
        {graph.groups.length === 0 ? (
          <EmptyState
            title={t("knowledge.meaning.noGroups")}
            hint={t("knowledge.meaning.noGroupsHint")}
          />
        ) : drawable.length === 0 ? (
          <EmptyState
            title={t("knowledge.meaning.nothingToDraw")}
            hint={t("knowledge.meaning.nothingToDrawHint")}
          />
        ) : (
          <div
            style={{
              position: "absolute",
              left: "50%",
              top: "50%",
              width: W,
              height: H,
              margin: `-${H / 2}px 0 0 -${W / 2}px`,
            }}
          >
            <svg viewBox={`0 0 ${W} ${H}`} style={{ width: W, height: H }}>
              {drawable.map(({ g, pts }) => {
                const main = g.id === focused?.id;
                return (
                  <path
                    key={g.id}
                    d={hullPath(pts, main ? 20 : 14)}
                    fill={g.color || "var(--ac)"}
                    fillOpacity={main ? 0.085 : 0.035}
                    stroke={g.color || "var(--ac)"}
                    strokeOpacity={main ? 0.85 : 0.4}
                    strokeWidth={main ? 1.6 : 1}
                    strokeDasharray={main ? "0" : "5 5"}
                    style={{ cursor: "pointer" }}
                    onClick={() => setFocus(g.id)}
                  />
                );
              })}
              {graph.relations.map((r) => {
                const a = drawable.find((d) => d.g.id === r.from);
                const b = drawable.find((d) => d.g.id === r.to);
                if (!a || !b) return null;
                const { path, head } = curveBetween(centroid(a.pts), centroid(b.pts), 26);
                const style = RELATION_STYLE[r.type] ?? RELATION_STYLE.references;
                const lit = focused && (r.from === focused.id || r.to === focused.id);
                return (
                  <g key={r.id} opacity={lit ? 1 : 0.35}>
                    <path
                      d={path}
                      fill="none"
                      stroke={style.color}
                      strokeOpacity={0.8}
                      strokeWidth={1.6}
                      strokeDasharray={style.dash}
                    />
                    <path d={head} fill={style.color} fillOpacity={0.9} />
                  </g>
                );
              })}
              {index.nodes.map((n) => {
                const p = pos.get(n.source);
                if (!p) return null;
                const inFocus = focused?.sources.includes(n.source);
                return (
                  <circle
                    key={n.source}
                    cx={p[0]}
                    cy={p[1]}
                    r={inFocus ? 4.4 : 3}
                    fill={inFocus ? focused?.color || "var(--ac)" : "var(--tx-dim)"}
                    fillOpacity={inFocus ? 0.95 : 0.45}
                  />
                );
              })}
            </svg>
            {drawable.map(({ g, pts }) => {
              const c = centroid(pts);
              return (
                <div
                  key={g.id}
                  style={{
                    position: "absolute",
                    left: c[0],
                    top: c[1] - 26,
                    transform: "translate(-50%,-100%)",
                    font: "600 10px 'IBM Plex Sans'",
                    color: g.color || "var(--ac)",
                    background: "var(--bg-deep)",
                    border: `1px solid ${g.color || "var(--ac)"}`,
                    borderRadius: 4,
                    padding: "2px 7px",
                    whiteSpace: "nowrap",
                    pointerEvents: "none",
                    opacity: g.id === focused?.id ? 1 : 0.55,
                  }}
                >
                  {g.name}
                </div>
              );
            })}
          </div>
        )}
      </div>

      <div style={{ ...panel, width: 300, flex: "none", borderLeft: "1px solid var(--bd)" }}>
        {!focused ? (
          <EmptyState title={t("knowledge.meaning.pickGroup")} hint={t("knowledge.meaning.pickGroupHint")} />
        ) : (
          <>
            {error ? (
              <div style={{ padding: "12px 14px" }}>
                <Notice tone="error">{error}</Notice>
              </div>
            ) : null}
            <GroupHeader group={focused} nodesInIndex={pointsOf(focused).length} />
            <ProjectLinks graph={graph} group={focused} onRun={run} busy={busy} />
            <Relations
              graph={graph}
              group={focused}
              relations={relationsOf(focused.id)}
              onRun={run}
              busy={busy}
            />
            <Section title={t("knowledge.meaning.groupNodes")} meta={String(focused.sources.length)}>
              {focused.sources.length === 0 ? (
                <span
                  style={{
                    font: "400 10.5px 'IBM Plex Sans'",
                    color: "var(--tx-dim)",
                    lineHeight: 1.7,
                  }}
                >
                  {t("knowledge.meaning.emptyGroup")}
                </span>
              ) : (
                focused.sources.map((s) => (
                  <div
                    key={s}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 7,
                      padding: "5px 7px",
                      borderRadius: 6,
                      background: "var(--bg-card)",
                    }}
                  >
                    <div
                      style={{
                        width: 6,
                        height: 6,
                        borderRadius: "50%",
                        background: pos.has(s) ? focused.color || "var(--ac)" : "var(--tx-faint)",
                        flex: "none",
                      }}
                    />
                    <span
                      style={{
                        font: "500 10px 'IBM Plex Mono'",
                        color: "var(--tx3)",
                        flex: 1,
                        minWidth: 0,
                        overflow: "hidden",
                        textOverflow: "ellipsis",
                        whiteSpace: "nowrap",
                      }}
                    >
                      {shortLabel(s)}
                    </span>
                    {!pos.has(s) ? (
                      <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--amber)" }}>
                        {t("knowledge.meaning.notIndexed")}
                      </span>
                    ) : null}
                  </div>
                ))
              )}
            </Section>
            <div style={{ padding: "12px 14px" }}>
              <Button
                tone="danger"
                disabled={busy}
                onClick={() => {
                  run(() => knowledge.deleteGroup(focused.id));
                  setFocus(null);
                }}
              >
                {t("knowledge.meaning.deleteGroup")}
              </Button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function GroupHeader({ group, nodesInIndex }: { group: KnowledgeGroup; nodesInIndex: number }) {
  const { t } = useTranslation();
  return (
    <div
      style={{
        padding: "13px 14px",
        borderBottom: "1px solid var(--bd-soft)",
        display: "flex",
        flexDirection: "column",
        gap: 9,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <Swatch color={group.color} size={10} />
        <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)", flex: 1 }}>
          {group.name}
        </span>
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
        <Box label="nodes" value={String(group.sources.length)} />
        <Box label={t("knowledge.meaning.indexed")} value={String(nodesInIndex)} />
      </div>
      <div style={{ display: "flex", alignItems: "baseline", gap: 7 }}>
        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>tag</span>
        <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx3)" }}>{group.id}</span>
      </div>
      <span style={{ font: "400 9.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>
        {t("knowledge.meaning.tagNote")}
      </span>
    </div>
  );
}

function Box({ label, value }: { label: string; value: string }) {
  return (
    <div
      style={{
        background: "var(--bg-card)",
        border: "1px solid var(--bd2)",
        borderRadius: 7,
        padding: "7px 8px",
        display: "flex",
        flexDirection: "column",
        gap: 2,
      }}
    >
      <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{label}</span>
      <span style={{ font: "600 13px 'IBM Plex Mono'", color: "var(--tx)" }}>{value}</span>
    </div>
  );
}

function ProjectLinks({
  graph,
  group,
  onRun,
  busy,
}: {
  graph: KnowledgeGraph;
  group: KnowledgeGroup;
  onRun: (fn: () => Promise<unknown>) => Promise<void>;
  busy: boolean;
}) {
  const { t } = useTranslation();
  const toggle = (pid: string) => {
    const next = group.projects.includes(pid)
      ? group.projects.filter((x) => x !== pid)
      : [...group.projects, pid];
    // Only projects are sent: omitting sources leaves them alone, where an
    // empty array would clear them.
    onRun(() => knowledge.setLinks(group.id, { projects: next }));
  };
  return (
    <Section title={t("knowledge.meaning.memberProjects")} meta={`${group.projects.length} / ${graph.projects.length}`}>
      {graph.projects.length === 0 ? (
        <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.7 }}>
          {t("knowledge.meaning.noProjects")}
        </span>
      ) : (
        <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
          {graph.projects.map((p) => (
            <div
              key={p.id}
              onClick={busy ? undefined : () => toggle(p.id)}
              style={pillStyle(group.projects.includes(p.id))}
            >
              {p.name}
            </div>
          ))}
        </div>
      )}
      <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.65 }}>
        {t("knowledge.meaning.multiProject")}
      </span>
    </Section>
  );
}

function Relations({
  graph,
  group,
  relations,
  onRun,
  busy,
}: {
  graph: KnowledgeGraph;
  group: KnowledgeGroup;
  relations: KnowledgeGraph["relations"];
  onRun: (fn: () => Promise<unknown>) => Promise<void>;
  busy: boolean;
}) {
  const { t } = useTranslation();
  const [adding, setAdding] = useState(false);
  const others = graph.groups.filter((g) => g.id !== group.id);

  return (
    <Section
      title="RELATIONS"
      meta={String(relations.length)}
      action={
        <Button
          tone="accent"
          disabled={busy || others.length === 0}
          onClick={() => setAdding(!adding)}
        >
          {adding ? t("common.close") : `+ ${t("common.add")}`}
        </Button>
      }
    >
      <span style={{ font: "400 9.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>
        {t("knowledge.meaning.relationNote")}
      </span>
      {adding ? (
        <div
          style={{
            background: "var(--bg-card)",
            border: "1px solid var(--bd2)",
            borderRadius: 9,
            padding: "9px 10px",
            display: "flex",
            flexDirection: "column",
            gap: 7,
          }}
        >
          <span style={{ ...railHeading, fontSize: 9 }}>{t("knowledge.meaning.pickOtherGroup")}</span>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 5 }}>
            {others.map((g) => (
              <div
                key={g.id}
                onClick={() => {
                  onRun(() => knowledge.addRelation(group.id, g.id, "references"));
                  setAdding(false);
                }}
                style={pillStyle(false, g.color || "var(--ac)")}
              >
                {g.name}
              </div>
            ))}
          </div>
        </div>
      ) : null}
      {relations.map((r) => {
        const otherId = r.from === group.id ? r.to : r.from;
        const other = graph.groups.find((g) => g.id === otherId);
        return (
          <div
            key={r.id}
            style={{
              background: "var(--bg-card)",
              border: "1px solid var(--bd2)",
              borderRadius: 9,
              padding: "9px 10px",
              display: "flex",
              flexDirection: "column",
              gap: 8,
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <span style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx3)" }}>
                {t(r.from === group.id ? "knowledge.meaning.fromThis" : "knowledge.meaning.toThis")}
              </span>
              <div style={{ flex: 1 }} />
              <div
                onClick={busy ? undefined : () => onRun(() => knowledge.deleteRelation(r.id))}
                style={{ cursor: "pointer", padding: "0 2px", color: "var(--tx-faint)" }}
              >
                ✕
              </div>
            </div>
            <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
              <Swatch color={other?.color ?? "var(--tx3)"} />
              <span
                style={{
                  font: "600 11.5px 'IBM Plex Sans'",
                  color: "var(--tx)",
                  flex: 1,
                  minWidth: 0,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {other?.name ?? otherId}
              </span>
            </div>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
              {RELATION_TYPES.map((rt) => (
                <div
                  key={rt}
                  onClick={
                    busy || rt === r.type
                      ? undefined
                      : () => onRun(() => knowledge.setRelationType(r.id, rt as RelationType))
                  }
                  title={t(`knowledge.meaning.means.${RELATION_MEANING[rt]}`)}
                  style={{
                    font: "600 8.8px 'IBM Plex Mono'",
                    cursor: "pointer",
                    padding: "3px 6px",
                    borderRadius: 4,
                    color: rt === r.type ? RELATION_STYLE[rt].color : "var(--tx-dim)",
                    background: rt === r.type ? "var(--bg-deep)" : "transparent",
                    border: `1px solid ${rt === r.type ? RELATION_STYLE[rt].color : "var(--bd2)"}`,
                  }}
                >
                  {rt}
                </div>
              ))}
            </div>
            {/* What the chosen type asserts. It is a claim about the two bodies
                of knowledge, not a permission — the task's scope decides what is
                reachable, and no edge changes it. */}
            <span style={{ font: "400 9px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.5 }}>
              {t(`knowledge.meaning.means.${RELATION_MEANING[r.type]}`)}
            </span>
          </div>
        );
      })}
    </Section>
  );
}
