import { useEffect, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { sectionTitle } from "./ui";
import { rag, RAG_SCOPES, RAG_MEDIA, type RagStatus, type RagResult, type RagSource } from "@/lib/rag";
import { knowledgeSources, isDesktop, type CaptionSetting, type KnowledgeSource } from "@/lib/knowledgeSources";

// SourcesCard registers what the indexer is allowed to read.
//
// This is the entry point to Knowledge: without a reference here there is
// nothing to embed, so the graph stays empty and rag_search returns nothing. It
// used to be reachable only by setting ORCHESTRA_KNOWLEDGE_DIR before launch,
// which is not a thing anyone discovers.
//
// It registers references and nothing more. Which group a reference belongs to,
// and which scope that group serves, are declared on the Knowledge screen —
// that is where the hierarchy is authored, and a second, shallower notion of
// scope here would give two places to answer the same question.
/** What the ON switch turns on. A vision model the shipped gateway already
 *  routes to, and cheap enough that describing a folder of screenshots is a
 *  decision rather than an event. The operator can point the generated config
 *  at another one; this is only the default the toggle writes. */
const DEFAULT_CAPTION_MODEL = "gpt-4o-mini";

function SourcesCard({ onChanged }: { onChanged: () => void }) {
  const { t } = useTranslation();
  const [sources, setSources] = useState<KnowledgeSource[] | null>(null);
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [caption, setCaption] = useState<CaptionSetting | null>(null);
  const desktop = isDesktop();

  useEffect(() => {
    if (!desktop) { setSources([]); return; }
    knowledgeSources.list().then(setSources).catch((e) => setErr(String(e)));
    knowledgeSources.caption().then(setCaption).catch(() => setCaption({ model: "", prefix: "" }));
  }, [desktop]);

  // Toggling restarts the indexer, exactly as adding a source does, because the
  // setting is read while ingesting rather than while searching.
  const setCaptionModel = async (model: string) => {
    setBusy(true); setErr(null);
    try {
      setCaption(await knowledgeSources.setCaption(model, caption?.prefix ?? ""));
      onChanged();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  // Every mutation restarts the indexer, so the call is slow by design and the
  // UI says what it is waiting for rather than looking hung.
  const apply = async (fn: () => Promise<KnowledgeSource[]>) => {
    setBusy(true); setErr(null);
    try {
      setSources(await fn());
      onChanged();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const addFolder = async () => {
    const picked = await knowledgeSources.pickFolder();
    if (picked) await apply(() => knowledgeSources.add("local", picked));
  };

  const addUrl = async () => {
    const u = url.trim();
    if (!u) return;
    await apply(async () => {
      const next = await knowledgeSources.add("external", u);
      setUrl("");
      return next;
    });
  };

  return (
    <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "14px 16px", display: "flex", flexDirection: "column", gap: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.rag.sourcesTitle")}</span>
        <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("settings.rag.sourcesHint")}</span>
        <div style={{ flex: 1 }} />
        <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{sources ? t("settings.rag.countUnit", { count: sources.length }) : "…"}</span>
      </div>

      {!desktop ? (
        <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "#d39a4e", lineHeight: 1.6 }}>
          {t("settings.rag.desktopOnly")}
        </span>
      ) : (
        <>
          <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>
            <Trans i18nKey="settings.rag.mountNote" components={{ b: <strong style={{ color: "var(--tx3)" }} />, code: <span style={{ font: "500 10px 'IBM Plex Mono'" }} /> }} />
          </span>
          {/* Which formats are read, and — more importantly — which are listed
              but not read. Someone who drops a folder of screenshots in here
              and sees them appear in the index will assume they are searchable
              unless it is said here, before they register the folder. */}
          <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>
            <Trans i18nKey="settings.rag.formatsNote" components={{ b: <strong style={{ color: "var(--tx3)" }} />, warn: <strong style={{ color: "#d39a4e" }} /> }} />
          </span>

          {/* Image captioning. Placed with the formats note it qualifies: that
              note is where someone learns pictures are not searchable, so this
              is where they should learn it is a choice. */}
          <div style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "9px 11px" }}>
            <div
              onClick={busy || !caption ? undefined : () => setCaptionModel(caption.model ? "" : DEFAULT_CAPTION_MODEL)}
              style={{
                font: "600 9px 'IBM Plex Mono'", flex: "none", cursor: busy ? "wait" : "pointer", padding: "3px 8px", borderRadius: 5,
                color: caption?.model ? "#06121e" : "var(--tx3)",
                background: caption?.model ? "#b08ad9" : "var(--bg-card2)",
                border: `1px solid ${caption?.model ? "#b08ad9" : "var(--bd2)"}`,
              }}
            >
              {caption?.model ? "ON" : "OFF"}
            </div>
            <div style={{ display: "flex", flexDirection: "column", gap: 2, flex: 1, minWidth: 0 }}>
              <span style={{ font: "600 10.5px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("settings.rag.captionTitle")}</span>
              <span style={{ font: "400 9.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.55 }}>
                {caption?.model ? t("settings.rag.captionOnNote", { model: caption.model }) : t("settings.rag.captionOffNote")}
              </span>
            </div>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
            {(sources ?? []).map((s) => (
              <div key={s.path} style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "9px 11px" }}>
                <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: s.kind === "external" ? "#e0a83e" : "#67c9a4", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 6px", borderRadius: 4, flex: "none" }}>
                  {t(s.kind === "external" ? "rag.kind.external" : "rag.kind.local")}
                </span>
                <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx3)", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.path}</span>
                <div onClick={() => !busy && apply(() => knowledgeSources.remove(s.path))} style={{ cursor: busy ? "wait" : "pointer", color: "var(--tx-mut)", font: "400 15px 'IBM Plex Sans'", padding: "0 4px", lineHeight: 1 }}>✕</div>
              </div>
            ))}
            {sources?.length === 0 && (
              <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>
                {t("settings.rag.noSources")}
              </span>
            )}
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <div onClick={() => !busy && addFolder()} style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 14px", borderRadius: 7, cursor: busy ? "wait" : "pointer" }}>
              {t("settings.rag.addFolder")}
            </div>
            <input
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && !busy && addUrl()}
              placeholder={t("settings.rag.urlPlaceholder")}
              spellCheck={false}
              style={{ flex: 1, minWidth: 220, background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "500 11px 'IBM Plex Mono'", color: "var(--tx)", outline: "none" }}
            />
            <div onClick={() => !busy && addUrl()} style={{ font: "600 11px 'IBM Plex Sans'", color: url.trim() ? "var(--ac)" : "var(--tx-faint)", padding: "8px 13px", border: "1px solid var(--bd2)", borderRadius: 7, cursor: url.trim() && !busy ? "pointer" : "not-allowed" }}>
              {t("common.add")}
            </div>
          </div>
          {busy && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("settings.rag.rebuilding")}</span>}
        </>
      )}
      {err && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</span>}
    </div>
  );
}

// KindBadge marks a source as a local mount or an external HTTPS document.
function KindBadge({ source }: { source: RagSource }) {
  const { t } = useTranslation();
  const external = source.kind === "external";
  const color = external ? "#e0a83e" : "#67c9a4";
  return (
    <span style={{ font: "600 8.5px 'IBM Plex Mono'", color, background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 6px", borderRadius: 4, flex: "none" }}>
      {t(external ? "rag.kind.external" : "rag.kind.local")}
    </span>
  );
}

// MediaBadge names the file class, but only when it is not plain text —
// stamping "text" on every Markdown file would be noise, while a PDF or a
// screenshot sitting in the list is exactly what the reader wants to spot.
function MediaBadge({ source }: { source: RagSource }) {
  const { t } = useTranslation();
  const media = source.media;
  if (!media || media === "text") return null;
  const m = RAG_MEDIA[media];
  return (
    <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: m.color, background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 6px", borderRadius: 4, flex: "none" }}>
      {t(m.labelKey)}
    </span>
  );
}

// What the chunk count actually means for this source. An image contributes a
// chunk too — its path and filename — and showing "1 chunks" next to a Markdown
// file's "1 chunks" would claim its contents are searchable. They are not, so
// this says so instead, and hangs the indexer's own note off the tooltip.
function ContentState({ source }: { source: RagSource }) {
  const { t } = useTranslation();
  const dim = { font: "400 9px 'IBM Plex Mono'", flex: "none" } as const;
  if (source.error) return <span title={source.error} style={{ ...dim, color: "var(--red)" }}>{t("settings.rag.fetchFailed")}</span>;
  if (source.content === "metadata") {
    return (
      <span title={source.note ?? t("settings.rag.metadataOnlyTip")} style={{ ...dim, color: "#d39a4e" }}>
        {t("settings.rag.pathOnly")}
      </span>
    );
  }
  // A caption is searchable, so it earns a chunk count — but it is a model's
  // description of the picture, not words the file contains, and saying "text"
  // about it would be the same overclaim in a quieter voice.
  if (source.content === "caption") {
    return (
      <span title={source.note ?? t("settings.rag.captionedTip")} style={{ ...dim, color: "#b08ad9" }}>
        {t("settings.rag.captioned")}
      </span>
    );
  }
  return (
    <span title={source.note} style={{ ...dim, color: source.note ? "#d39a4e" : "var(--tx-faint)" }}>
      {source.chunks} chunks{source.note ? " ⚠" : ""}
    </span>
  );
}

export function RagPanel() {
  const { t } = useTranslation();
  const [status, setStatus] = useState<RagStatus | null>(null);
  const [online, setOnline] = useState<boolean | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<RagResult[] | null>(null);
  const timer = useRef<number | null>(null);

  const refresh = async () => {
    if (!(await rag.health())) { setOnline(false); return; }
    setOnline(true);
    try { setStatus(await rag.status()); setErr(null); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
  };

  useEffect(() => {
    refresh();
    timer.current = window.setInterval(refresh, 3000);
    return () => { if (timer.current) window.clearInterval(timer.current); };
  }, []);

  const reindex = async () => {
    setBusy("index");
    try { await rag.reindex(); } catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(null); }
  };

  const search = async () => {
    if (!query.trim()) return;
    setBusy("search"); setResults(null); setErr(null);
    try { setResults((await rag.search(query, 5)).results); }
    catch (e) { setErr(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(null); }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
      {sectionTitle(t("settings.nav.rag"), t("settings.rag.desc"))}

      {online === false && (
        <div style={{ font: "400 11px 'IBM Plex Sans'", color: "#d39a4e", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "10px 13px" }}>
          {t("settings.rag.indexerOffline")}
        </div>
      )}
      {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "9px 12px" }}>{err}</div>}

      {/* Nothing else on this screen reveals which vectors the index holds, and
          the two differ enormously in what a search comes back with. An index
          built without a model has to say so wherever its results are shown. */}
      {status?.embedMode === "offline" && (
        <div style={{ font: "400 11px 'IBM Plex Sans'", color: "#d39a4e", background: "var(--bg-card)", border: "1px solid var(--tint-amber-bd, var(--bd2))", borderRadius: 9, padding: "10px 13px", lineHeight: 1.6 }}>
          <Trans i18nKey="settings.rag.offlineEmbed" components={{ b: <strong />, code: <span style={{ font: "500 10px 'IBM Plex Mono'" }} /> }} />
        </div>
      )}

      <SourcesCard onChanged={refresh} />

      {/* status */}
      <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "14px 16px", display: "flex", flexDirection: "column", gap: 12 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <div style={{ width: 8, height: 8, borderRadius: "50%", background: online ? "#3fbf8f" : "var(--tx-faint)" }} />
          <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.rag.indexTitle")}</span>
          <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
            {status ? `${status.chunks} chunks · ${status.sources.length} files` : "…"}{status?.building ? ` · ${t("settings.rag.building")}` : ""}{!status?.building && status?.reused ? ` · ${t("settings.rag.reusedCount", { reused: status.reused, embedded: status.embedded ?? 0 })}` : ""}
          </span>
          <div style={{ flex: 1 }} />
          {status?.builtAt && <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.rag.updatedAt")} {status.builtAt.replace("T", " ").replace("Z", "")}</span>}
          <div onClick={() => online && !busy && reindex()} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--ac)", cursor: online ? "pointer" : "not-allowed", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)" }}>{busy === "index" || status?.building ? t("settings.rag.reindexing") : t("settings.rag.reindex")}</div>
        </div>
        {status?.lastError && <div style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--red)" }}>{t("settings.rag.indexError")}: {status.lastError}</div>}
        {status && status.sources.length > 0 && (
          <div style={{ display: "flex", flexDirection: "column", gap: 12, maxHeight: 320, overflowY: "auto" }}>
            {RAG_SCOPES.map((scope) => {
              const items = status.sources.filter((s) => (s.scope ?? "project") === scope.id);
              if (items.length === 0) return null;
              return (
                <div key={scope.id} style={{ display: "flex", flexDirection: "column", gap: 5 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                    <span style={{ width: 6, height: 6, borderRadius: "50%", background: scope.color, flex: "none" }} />
                    <span style={{ font: "600 10px 'IBM Plex Sans'", color: "var(--tx2)" }}>{t(scope.labelKey)}</span>
                    <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{scope.hint}</span>
                    <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>· {items.length}</span>
                  </div>
                  {items.map((s) => (
                    <div key={(s.url ?? "") + s.path} style={{ display: "flex", alignItems: "center", gap: 8, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "7px 11px" }}>
                      <KindBadge source={s} />
                      <MediaBadge source={s} />
                      <span title={s.url ?? s.path} style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx2)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{s.path}</span>
                      <ContentState source={s} />
                    </div>
                  ))}
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* test search */}
      <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "14px 16px", display: "flex", flexDirection: "column", gap: 11 }}>
        <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.rag.testSearch")}</span>
        <div style={{ display: "flex", gap: 8 }}>
          <input value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={(e) => e.key === "Enter" && search()} placeholder={t("settings.rag.searchPlaceholder")} style={{ flex: 1, background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }} />
          <div onClick={() => !busy && search()} style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 16px", borderRadius: 7, cursor: "pointer" }}>{busy === "search" ? t("settings.rag.searching") : t("common.search")}</div>
        </div>
        {results && results.length === 0 && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.rag.noHits")}</span>}
        {results && results.map((r, i) => (
          <div key={i} style={{ display: "flex", flexDirection: "column", gap: 4, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 8, padding: "9px 12px" }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span style={{ font: "600 10px 'IBM Plex Mono'", color: "#5b9fe8" }}>{r.source}</span>
              <div style={{ flex: 1 }} />
              <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>score {r.score.toFixed(3)}</span>
            </div>
            <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.5, maxHeight: 66, overflow: "hidden" }}>{r.text}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
