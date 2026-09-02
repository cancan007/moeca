import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { cardStyle, sectionTitle, segStyle } from "./ui";
import { useStore } from "@/store/useStore";
import { sandbox, type ImagePolicy, type WebSearch } from "@/lib/sandbox";
import {
  type SoloAgent,
  type StaticTemplate,
  type GraphTemplate,
  type SupervisorTemplate,
  archChips,
  archExample,
  soloSystem,
} from "@/lib/templates";

/* ─────────────────────────── helpers ─────────────────────────── */

/** Reads a positive integer from a text field, or undefined for "leave it to
 *  the default" — an empty box and a nonsense one mean the same thing here. */
function positiveInt(v: string): number | undefined {
  const n = parseInt(v, 10);
  return Number.isFinite(n) && n > 0 ? n : undefined;
}

function slug(name: string): string {
  const s = name.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  return s || `agent-${Math.floor(Date.now() % 100000)}`;
}

/* ─────────────────────────── role note ─────────────────────────── */

function RoleNote({ dot, tag, tagColor, desc }: { dot: string; tag: string; tagColor: string; desc: string }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 9, padding: "11px 14px" }}>
      <div style={{ width: 9, height: 9, borderRadius: "50%", background: dot, flex: "none" }} />
      <span style={{ font: "600 9px 'IBM Plex Mono'", color: tagColor, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", padding: "2px 8px", borderRadius: 5, flex: "none" }}>{tag}</span>
      <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx2)", lineHeight: 1.5 }}>{desc}</span>
    </div>
  );
}

/* ─────────────────────────── agent edit modal ─────────────────────────── */

function AgentEditModal({ base, onClose }: { base: SoloAgent | null; onClose: () => void }) {
  const { t } = useTranslation();
  const upsertSolo = useStore((s) => s.upsertSolo);
  const deleteSolo = useStore((s) => s.deleteSolo);
  const modelProviders = useStore((s) => s.providers).filter((p) => p.kind === "model");
  const allTools = useStore((s) => s.tools);
  const [toolIds, setToolIds] = useState<string[]>(base?.toolIds ?? []);
  const toggleTool = (id: string) => setToolIds((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]));
  const [useRag, setUseRag] = useState<boolean>(base?.useRag ?? false);
  // How far this agent may follow knowledge relations out of the task's scope.
  // Only meaningful with RAG: it widens what rag_search may reach.
  // Media generation is no longer a switch here. Image, speech and video are
  // ordinary tools now (Settings → Tools), granted like any other, so this
  // screen has one list of capabilities instead of two that behaved
  // differently and could not name the same providers.
  // Web search. No route to configure — the provider runs the search — so the
  // only knob is the budget, and it is a budget rather than a model id because
  // this is the one tool billed per call instead of per token.
  const [web, setWeb] = useState<WebSearch | undefined>(base?.web);
  const [webMaxUses, setWebMaxUses] = useState<string>(base?.web?.maxUses != null ? String(base.web.maxUses) : "");
  const [name, setName] = useState(base?.name ?? "");
  const [role, setRole] = useState(base?.role ?? "");
  const [providerId, setProviderId] = useState(base?.providerId ?? modelProviders[0]?.name ?? "anthropic");
  const provider = modelProviders.find((p) => p.name === providerId) ?? modelProviders[0];
  const modelOptions = provider?.models ?? [];
  const [model, setModel] = useState(base?.model ?? modelOptions[0] ?? "");
  const [ctx, setCtx] = useState(base?.ctx ?? "128k");
  const [strat, setStrat] = useState(base?.strat ?? t("templates.strat.compressStandard"));
  const [effort, setEffort] = useState<string>(base?.effort ?? "");
  const [maxTokens, setMaxTokens] = useState<string>(base?.maxTokens != null ? String(base.maxTokens) : "");
  const [arch, setArch] = useState(base?.arch ?? "generic");
  const [tab, setTab] = useState<"edit" | "example">("edit");
  const [prompt, setPrompt] = useState(base ? soloSystem(base) : "");
  const example = archExample(arch);

  // Command atoms: which sandbox image, and what to run in it. The image list
  // comes from the controller rather than being hardcoded, so a custom image
  // added under Settings → Sandbox shows up here without a second edit —
  // and so does whether it is approved for unattended (Daily) runs.
  const [kind, setKind] = useState<"agent" | "command">(base?.kind ?? "agent");
  const [image, setImage] = useState(base?.image ?? "");
  const [cmd, setCmd] = useState(base?.cmd ?? "");
  const [images, setImages] = useState<ImagePolicy[]>([]);
  useEffect(() => {
    sandbox.images().then((r) => setImages(r.images)).catch(() => setImages([]));
  }, []);
  const chosenImage = images.find((i) => i.name === (image || "base"));

  // When switching provider, snap the model to one the provider offers.
  const onProvider = (pid: string) => {
    setProviderId(pid);
    const p = modelProviders.find((x) => x.name === pid);
    if (p && !p.models.includes(model)) setModel(p.models[0] ?? "");
  };

  const save = () => {
    const id = base?.id ?? slug(name || role || "agent");
    const dot = base?.dot ?? "#8fa3b8";
    const mt = parseInt(maxTokens, 10);
    upsertSolo({
      id, name: name || "Agent", role, providerId, model, ctx, strat, dot, arch,
      effort: effort || undefined,
      maxTokens: Number.isFinite(mt) && mt > 0 ? mt : undefined,
      system: prompt || undefined, toolIds, useRag,
      // Only meaningful with RAG, and 0 is the same as unset — a stored zero
      // would claim a setting was made when nothing was.
      web: web ? { ...web, maxUses: positiveInt(webMaxUses) } : undefined,
      // A command atom keeps its model fields (so switching back to "agent"
      // does not lose them) but they are dropped at compile time.
      kind,
      image: kind === "command" ? image || undefined : base?.image,
      cmd: kind === "command" ? cmd.trim() || undefined : base?.cmd,
    });
    onClose();
  };

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", zIndex: 60, display: "flex", alignItems: "center", justifyContent: "center", padding: 30 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 880, maxWidth: "100%", maxHeight: "100%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, boxShadow: "0 24px 80px rgba(0,0,0,.5)", display: "flex", flexDirection: "column", overflow: "hidden" }}>
        <div style={{ flex: "none", padding: "15px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 11 }}>
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="var(--ac)" strokeWidth="1.6"><circle cx="8" cy="5" r="2.5" /><path d="M3 13c0-2.5 2.2-4 5-4s5 1.5 5 4" /></svg>
          <span style={{ font: "700 15px 'IBM Plex Sans'", color: "var(--tx)" }}>{base ? t("settings.agents.editAgent", { name: base.name }) : t("settings.agents.newAgent")}</span>
          <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "#67c9a4", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 8px", borderRadius: 5 }}>Solo · {t("settings.agents.soloTag")}</span>
          <div style={{ flex: 1 }} />
          {base && <div onClick={() => { deleteSolo(base.id); onClose(); }} style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--red)", cursor: "pointer", padding: "4px 10px", border: "1px solid var(--tint-red-bd)", borderRadius: 6 }}>{t("common.delete")}</div>}
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 19px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
        </div>

        <div style={{ flex: 1, overflowY: "auto", padding: "18px 22px", display: "flex", flexDirection: "column", gap: 16 }}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
            <Field label={t("settings.agents.fieldName")}><input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("settings.agents.namePlaceholder")} style={inputStyle} /></Field>
            <Field label={t("settings.agents.fieldRole")}><input value={role} onChange={(e) => setRole(e.target.value)} placeholder={t("settings.agents.rolePlaceholder")} style={inputStyle} /></Field>
          </div>

          {/* What this atom is. Both shapes become one stage of the DAG, so a
              Graph can mix them freely: research (agent) → build (command). */}
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.fieldKind")}</span>
            <div style={{ display: "flex", gap: 8 }}>
              {(["agent", "command"] as const).map((k) => (
                <div key={k} onClick={() => setKind(k)} style={segStyle(kind === k)}>
                  <div style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx2)" }}>{t(`settings.agents.kinds.${k}.title`)}</div>
                  <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)", marginTop: 3 }}>{t(`settings.agents.kinds.${k}.sub`)}</div>
                </div>
              ))}
            </div>
          </div>

          {kind === "command" ? (
            <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
              <Field label={t("settings.agents.fieldImage")}>
                <select value={image} onChange={(e) => setImage(e.target.value)} style={{ ...inputStyle, fontFamily: "'IBM Plex Mono',monospace", colorScheme: "dark", cursor: "pointer" }}>
                  <option value="">{t("settings.agents.imageDefault")}</option>
                  {images.map((i) => (
                    <option key={i.name} value={i.name}>
                      {i.name}{i.network === "none" ? " · network none" : ""}{i.unattended ? "" : ` · ${t("settings.agents.dailyNotAllowed")}`}
                    </option>
                  ))}
                </select>
              </Field>
              {chosenImage && (
                <div style={{ display: "flex", flexDirection: "column", gap: 4, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "9px 11px" }}>
                  <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{chosenImage.description || chosenImage.ref}</span>
                  {chosenImage.name === "base" && (
                    <span style={{ font: "400 10px 'IBM Plex Mono'", color: "#e06a6a" }}>
                      {t("settings.agents.imageBaseNoShell")}
                    </span>
                  )}
                  {!chosenImage.unattended && (
                    <span style={{ font: "400 10px 'IBM Plex Mono'", color: "#d39a4e" }}>
                      {t("settings.agents.imageNotUnattended")}
                    </span>
                  )}
                  {chosenImage.network === "none" && (
                    <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
                      {t("settings.agents.imageNoNetwork")}
                    </span>
                  )}
                </div>
              )}
              <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
                  <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.agents.command")}</span>
                  <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.commandHint")}</span>
                </div>
                <textarea value={cmd} onChange={(e) => setCmd(e.target.value)} spellCheck={false} placeholder={t("settings.agents.commandPlaceholder")} style={promptStyle} />
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", lineHeight: 1.6 }}>
                  {t("settings.agents.commandNote")}
                </span>
              </div>
            </div>
          ) : (
          <>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
            <Field label={t("settings.agents.fieldProvider")}>
              <select value={providerId} onChange={(e) => onProvider(e.target.value)} style={{ ...inputStyle, fontFamily: "'IBM Plex Mono',monospace", colorScheme: "dark", cursor: "pointer" }}>
                {modelProviders.length === 0 && <option value="">{t("settings.agents.noProvider")}</option>}
                {modelProviders.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
              </select>
            </Field>
            <Field label={t("settings.agents.fieldModel")}>
              <select value={model} onChange={(e) => setModel(e.target.value)} style={{ ...inputStyle, fontFamily: "'IBM Plex Mono',monospace", colorScheme: "dark", cursor: "pointer" }}>
                {modelOptions.length === 0 && <option value="">—</option>}
                {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
            </Field>
            <div style={{ display: "flex", gap: 12 }}>
              <Field label={t("settings.agents.fieldCtx")}><input value={ctx} onChange={(e) => setCtx(e.target.value)} placeholder={t("settings.agents.ctxPlaceholder")} style={{ ...inputStyle, fontFamily: "'IBM Plex Mono',monospace" }} /></Field>
              <Field label={t("settings.agents.fieldStrat")}><input value={strat} onChange={(e) => setStrat(e.target.value)} placeholder="RAG: on" style={{ ...inputStyle, fontFamily: "'IBM Plex Mono',monospace" }} /></Field>
            </div>
            <div style={{ display: "flex", gap: 12 }}>
              <Field label={t("settings.agents.fieldEffort")}>
                <select value={effort} onChange={(e) => setEffort(e.target.value)} title={t("settings.agents.effortTip")} style={{ ...inputStyle, fontFamily: "'IBM Plex Mono',monospace", colorScheme: "dark", cursor: "pointer" }}>
                  <option value="">{t("settings.agents.effortDefault")}</option>
                  <option value="low">{t("settings.agents.effortLow")}</option>
                  <option value="medium">medium</option>
                  <option value="high">high</option>
                  <option value="xhigh">xhigh</option>
                  <option value="max">{t("settings.agents.effortMax")}</option>
                </select>
              </Field>
              <Field label={t("settings.agents.fieldMaxTokens")}>
                <input value={maxTokens} onChange={(e) => setMaxTokens(e.target.value)} placeholder={t("settings.agents.maxTokensPlaceholder")} inputMode="numeric" style={{ ...inputStyle, fontFamily: "'IBM Plex Mono',monospace" }} />
              </Field>
            </div>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.fieldArch")}</span>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 7 }}>
              {archChips().map((c) => (
                <div key={c.id} onClick={() => setArch(c.id)} style={chipStyle(arch === c.id)}>{c.label}</div>
              ))}
            </div>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.fieldTools")}</span>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 7 }}>
              <div onClick={() => setUseRag((v) => !v)} title={t("settings.agents.ragTip")} style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer", font: "500 10.5px 'IBM Plex Mono'", color: useRag ? "#06121e" : "var(--tx3)", background: useRag ? "#67c9a4" : "var(--bg-card2)", border: `1px solid ${useRag ? "#67c9a4" : "var(--bd2)"}`, borderRadius: 7, padding: "5px 10px" }}>
                {useRag ? "✓ " : ""}{t("settings.agents.ragLabel")}
              </div>
              <div
                onClick={() => setWeb((w) => (w ? undefined : {}))}
                title={t("settings.agents.webTip")}
                style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer", font: "500 10.5px 'IBM Plex Mono'", color: web ? "#06121e" : "var(--tx3)", background: web ? "#5aa9e6" : "var(--bg-card2)", border: `1px solid ${web ? "#5aa9e6" : "var(--bd2)"}`, borderRadius: 7, padding: "5px 10px" }}
              >
                {web ? "✓ " : ""}{t("settings.agents.webLabel")}
              </div>
              {web && (
                <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                  <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.webMaxUses")}</span>
                  <input
                    value={webMaxUses}
                    onChange={(e) => setWebMaxUses(e.target.value)}
                    placeholder={t("settings.agents.webMaxUsesPlaceholder")}
                    inputMode="numeric"
                    style={{ width: 70, background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 6, padding: "4px 7px", font: "400 10px 'IBM Plex Mono'", color: "var(--tx2)", outline: "none" }}
                  />
                </div>
              )}
              {allTools.length === 0 ? (
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.noTools")}</span>
              ) : (
                allTools.map((tool) => {
                  const on = toolIds.includes(tool.id);
                  return (
                    <div key={tool.id} onClick={() => toggleTool(tool.id)} style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer", font: "500 10.5px 'IBM Plex Mono'", color: on ? "#06121e" : "var(--tx3)", background: on ? "#3fbf8f" : "var(--bg-card2)", border: `1px solid ${on ? "#3fbf8f" : "var(--bd2)"}`, borderRadius: 7, padding: "5px 10px" }}>
                      {on ? "✓ " : ""}{tool.name}
                    </div>
                  );
                })
              )}
            </div>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
              <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.agents.systemPrompt")}</span>
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.systemPromptHint")}</span>
              <div style={{ flex: 1 }} />
              <TabSwitch tab={tab} setTab={setTab} />
            </div>
            {tab === "edit" ? (
              <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} spellCheck={false} placeholder={t("settings.agents.systemPromptPlaceholder")} style={promptStyle} />
            ) : (
              <ExampleBlock text={example} note={t("settings.agents.soloExampleNote")} onInsert={() => { setPrompt(example); setTab("edit"); }} />
            )}
          </div>
          </>
          )}
        </div>

        <div style={{ flex: "none", padding: "13px 20px", borderTop: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>ID: {base?.id ?? slug(name || "new-agent")}</span>
          <div style={{ flex: 1 }} />
          <div onClick={onClose} style={cancelBtn}>{t("common.cancel")}</div>
          <div onClick={save} style={saveBtn}>{base ? t("common.save") : t("settings.agents.register")}</div>
        </div>
      </div>
    </div>
  );
}

/* ─────────────────────────── static (graph DAG / supervisor) edit modal ─────────────────────────── */

interface DraftNode { key: string; soloId?: string; templateId?: string; deps: string[] }

function StaticEditModal({ base, onClose }: { base: StaticTemplate | null; onClose: () => void }) {
  const { t } = useTranslation();
  const staticGraphExample = t("settings.agents.graphExample");
  const staticSupExample = t("settings.agents.supExample");
  const solos = useStore((s) => s.solos);
  const staticTpls = useStore((s) => s.staticTpls);
  const upsert = useStore((s) => s.upsertStaticTpl);
  const del = useStore((s) => s.deleteStaticTpl);
  const soloById = (id: string) => solos.find((s) => s.id === id);
  const tplById = (id: string) => staticTpls.find((x) => x.id === id);
  // Templates usable as nodes (exclude self to avoid a trivial self-cycle).
  const nodeTemplates = staticTpls.filter((x) => x.id !== base?.id);

  const [name, setName] = useState(base?.name ?? "");
  const [desc, setDesc] = useState(base?.desc ?? "");
  const [pattern, setPattern] = useState<"graph" | "supervisor">(base?.pattern ?? "graph");
  const [nodes, setNodes] = useState<DraftNode[]>(
    base?.pattern === "graph"
      ? base.nodes.map((n) => ({ key: n.id, soloId: n.soloId, templateId: n.templateId, deps: base.edges.filter(([, to]) => to === n.id).map(([f]) => f) }))
      : [],
  );
  const [sup, setSup] = useState<string | null>(base?.pattern === "supervisor" ? base.supervisor : null);
  const [workers, setWorkers] = useState<string[]>(base?.pattern === "supervisor" ? base.workers : []);
  const [tab, setTab] = useState<"edit" | "example">("edit");
  const [prompt, setPrompt] = useState(base ? (base.pattern === "graph" ? staticGraphExample : staticSupExample) : "");

  const example = pattern === "graph" ? staticGraphExample : staticSupExample;
  const patColor = pattern === "graph" ? "#4f9dff" : "#b08ad9";

  const uniqueKey = (baseId: string) => {
    let k = baseId, i = 2;
    const keys = new Set(nodes.map((n) => n.key));
    while (keys.has(k)) k = `${baseId}-${i++}`;
    return k;
  };
  const addNode = (soloId: string) =>
    setNodes((n) => [...n, { key: uniqueKey(soloId), soloId, deps: n.length ? [n[n.length - 1].key] : [] }]);
  const addTemplateNode = (templateId: string) =>
    setNodes((n) => [...n, { key: uniqueKey(templateId), templateId, deps: n.length ? [n[n.length - 1].key] : [] }]);
  const nodeLabel = (n: DraftNode) => n.templateId ? (tplById(n.templateId)?.name ?? n.templateId) : (soloById(n.soloId ?? "")?.name ?? n.soloId ?? "");
  const nodeDot = (n: DraftNode) => n.templateId ? "#b08ad9" : (soloById(n.soloId ?? "")?.dot ?? "var(--tx-faint)");
  const removeNode = (key: string) =>
    setNodes((n) => n.filter((x) => x.key !== key).map((x) => ({ ...x, deps: x.deps.filter((d) => d !== key) })));
  const toggleDep = (key: string, dep: string) =>
    setNodes((n) => n.map((x) => (x.key === key ? { ...x, deps: x.deps.includes(dep) ? x.deps.filter((d) => d !== dep) : [...x.deps, dep] } : x)));

  const supPlaced = sup ? [sup, ...workers] : workers;
  const supPalette = solos.filter((s) => !supPlaced.includes(s.id));
  const addToSupervisor = (id: string) => { if (!sup) setSup(id); else if (!workers.includes(id)) setWorkers((w) => [...w, id]); };

  const save = () => {
    const id = base?.id ?? slug(name || "template");
    if (pattern === "graph") {
      const edges: [string, string][] = [];
      for (const n of nodes) for (const d of n.deps) edges.push([d, n.key]);
      const tpl: GraphTemplate = { id, name: name || "Graph", desc, pattern: "graph", nodes: nodes.map((n) => (n.templateId ? { id: n.key, templateId: n.templateId } : { id: n.key, soloId: n.soloId })), edges };
      upsert(tpl);
    } else {
      if (!sup) return;
      const tpl: SupervisorTemplate = { id, name: name || "Supervisor", desc, pattern: "supervisor", supervisor: sup, workers };
      upsert(tpl);
    }
    onClose();
  };

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", zIndex: 60, display: "flex", alignItems: "center", justifyContent: "center", padding: 30 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 900, maxWidth: "100%", maxHeight: "100%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, boxShadow: "0 24px 80px rgba(0,0,0,.5)", display: "flex", flexDirection: "column", overflow: "hidden" }}>
        <div style={{ flex: "none", padding: "15px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 11 }}>
          <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="var(--ac)" strokeWidth="1.6"><circle cx="4" cy="4" r="2" /><circle cx="12" cy="4" r="2" /><circle cx="8" cy="12" r="2" /><path d="M5.5 5.2 7 10M10.5 5.2 9 10" /></svg>
          <span style={{ font: "700 15px 'IBM Plex Sans'", color: "var(--tx)" }}>{base ? t("settings.agents.editTemplate", { name: base.name }) : t("settings.agents.newTemplate")}</span>
          <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: patColor, background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 8px", borderRadius: 5 }}>{pattern === "graph" ? "Graph · DAG" : `Supervisor · ${t("settings.agents.centralised")}`}</span>
          <div style={{ flex: 1 }} />
          {base && <div onClick={() => { del(base.id); onClose(); }} style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--red)", cursor: "pointer", padding: "4px 10px", border: "1px solid var(--tint-red-bd)", borderRadius: 6 }}>{t("common.delete")}</div>}
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 19px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
        </div>

        <div style={{ flex: 1, overflowY: "auto", padding: "18px 22px", display: "flex", flexDirection: "column", gap: 16 }}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
            <Field label={t("settings.agents.fieldName")}><input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("settings.agents.tplNamePlaceholder")} style={inputStyle} /></Field>
            <Field label={t("settings.agents.fieldDesc")}><input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder={t("settings.agents.tplDescPlaceholder")} style={inputStyle} /></Field>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.pattern")}</span>
            <div style={{ display: "flex", gap: 9 }}>
              <div onClick={() => setPattern("graph")} style={patternBtn(pattern === "graph", "#4f9dff")}>{t("settings.agents.patternGraph")}</div>
              <div onClick={() => setPattern("supervisor")} style={patternBtn(pattern === "supervisor", "#b08ad9")}>{t("settings.agents.patternSupervisor")}</div>
            </div>
          </div>

          {/* palette */}
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px" }}>{t("settings.agents.soloPalette")}</span>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 7, minHeight: 34, padding: 7, background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 9 }}>
              {(pattern === "graph" ? solos : supPalette).length === 0 ? (
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", padding: "4px 6px" }}>{t(pattern === "graph" ? "settings.agents.registerSoloFirst" : "settings.agents.allAgentsPlaced")}</span>
              ) : (
                (pattern === "graph" ? solos : supPalette).map((p) => (
                  <div key={p.id} onClick={() => (pattern === "graph" ? addNode(p.id) : addToSupervisor(p.id))} style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer", font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx2)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "5px 9px" }}>
                    <div style={{ width: 7, height: 7, borderRadius: "50%", background: p.dot }} />
                    {p.name}
                  </div>
                ))
              )}
            </div>
            {pattern === "graph" && nodeTemplates.length > 0 && (
              <>
                <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "#b08ad9", letterSpacing: "0.4px", marginTop: 4 }}>{t("settings.agents.tplPalette")}</span>
                <div style={{ display: "flex", flexWrap: "wrap", gap: 7, padding: 7, background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 9 }}>
                  {nodeTemplates.map((tpl) => (
                    <div key={tpl.id} onClick={() => addTemplateNode(tpl.id)} style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer", font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx2)", background: "var(--tint-purple)", border: "1px solid #6d5499", borderRadius: 7, padding: "5px 9px" }}>
                      <div style={{ width: 7, height: 7, borderRadius: "50%", background: "#b08ad9" }} />
                      {tpl.name}<span style={{ font: "400 8px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{tpl.pattern}</span>
                    </div>
                  ))}
                </div>
              </>
            )}
          </div>

          {pattern === "graph" ? (
            <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "#4f9dff", letterSpacing: "0.4px" }}>{t("settings.agents.nodesTitle")}</span>
              <div style={{ display: "flex", flexDirection: "column", gap: 8, minHeight: 60, padding: 12, background: "var(--bg-deep)", border: "1px dashed var(--bd3)", borderRadius: 10 }}>
                {nodes.length === 0 && <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("settings.agents.nodesEmpty")}</span>}
                {nodes.map((n, i) => {
                  const s = soloById(n.soloId ?? "");
                  return (
                    <div key={n.key} style={{ display: "flex", flexDirection: "column", gap: 6, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "9px 11px" }}>
                      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                        <span style={{ font: "700 8px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{i + 1}</span>
                        <div style={{ width: 8, height: 8, borderRadius: "50%", background: nodeDot(n) }} />
                        <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx)" }}>{nodeLabel(n)}</span>
                        <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{n.templateId ? t("settings.agents.nestedTemplate") : s?.model}</span>
                        <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>id: {n.key}</span>
                        <div style={{ flex: 1 }} />
                        <div onClick={() => removeNode(n.key)} style={{ cursor: "pointer", color: "var(--tx-faint)", font: "400 13px 'IBM Plex Sans'", padding: "0 2px" }}>×</div>
                      </div>
                      {nodes.length > 1 && (
                        <div style={{ display: "flex", alignItems: "center", gap: 6, flexWrap: "wrap", paddingLeft: 18 }}>
                          <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.deps")}</span>
                          {nodes.filter((o) => o.key !== n.key).map((o) => {
                            const on = n.deps.includes(o.key);
                            return (
                              <div key={o.key} onClick={() => toggleDep(n.key, o.key)} style={{ font: "500 9px 'IBM Plex Mono'", cursor: "pointer", padding: "2px 7px", borderRadius: 5, color: on ? "#06121e" : "var(--tx3)", background: on ? "#4f9dff" : "var(--bg-inset2)", border: `1px solid ${on ? "#4f9dff" : "var(--bd3)"}` }}>{o.key}</div>
                            );
                          })}
                          {n.deps.length === 0 && <span style={{ font: "400 8.5px 'IBM Plex Mono'", color: "#67c9a4" }}>{t("settings.agents.rootNode")}</span>}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
              <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "#b08ad9", letterSpacing: "0.4px" }}>{t("settings.agents.supervisorTitle")}</span>
              <div style={{ minHeight: 52, padding: "11px 13px", background: "var(--tint-purple)", border: "1px dashed #6d5499", borderRadius: 10, display: "flex", alignItems: "center", gap: 9 }}>
                {sup ? (
                  <div style={{ display: "flex", alignItems: "center", gap: 9, flex: 1 }}>
                    <div style={{ width: 8, height: 8, borderRadius: "50%", background: soloById(sup)?.dot ?? "var(--tx-faint)" }} />
                    <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{soloById(sup)?.name ?? sup}</span>
                    <div onClick={() => setSup(null)} style={{ marginLeft: "auto", cursor: "pointer", color: "var(--tx-faint)", font: "400 14px 'IBM Plex Sans'" }}>×</div>
                  </div>
                ) : (
                  <span style={{ font: "400 10px 'IBM Plex Sans'", color: "#c79ae0" }}>{t("settings.agents.supervisorEmpty")}</span>
                )}
              </div>
              <span style={{ font: "600 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px" }}>{t("settings.agents.workersTitle")}</span>
              <div style={{ display: "flex", flexWrap: "wrap", gap: 8, minHeight: 44, padding: 11, background: "var(--bg-deep)", border: "1px dashed var(--bd3)", borderRadius: 10 }}>
                {workers.length === 0 && <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("settings.agents.workersEmpty")}</span>}
                {workers.map((id) => {
                  const s = soloById(id);
                  return (
                    <div key={id} style={{ display: "flex", alignItems: "center", gap: 8, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "8px 11px" }}>
                      <div style={{ width: 8, height: 8, borderRadius: "50%", background: s?.dot ?? "var(--tx-faint)" }} />
                      <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx)" }}>{s?.name ?? id}</span>
                      <div onClick={() => setWorkers((w) => w.filter((x) => x !== id))} style={{ cursor: "pointer", color: "var(--tx-faint)", font: "400 13px 'IBM Plex Sans'" }}>×</div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
              <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.agents.systemPrompt")}</span>
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.tplPromptHint")}</span>
              <div style={{ flex: 1 }} />
              <TabSwitch tab={tab} setTab={setTab} />
            </div>
            {tab === "edit" ? (
              <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} spellCheck={false} placeholder={t("settings.agents.tplPromptPlaceholder")} style={promptStyle} />
            ) : (
              <ExampleBlock text={example} note={t("settings.agents.tplExampleNote", { pattern: pattern === "graph" ? "Graph" : "Supervisor" })} onInsert={() => { setPrompt(example); setTab("edit"); }} />
            )}
          </div>
        </div>

        <div style={{ flex: "none", padding: "13px 20px", borderTop: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>ID: {base?.id ?? slug(name || "new-template")}</span>
          <div style={{ flex: 1 }} />
          <div onClick={onClose} style={cancelBtn}>{t("common.cancel")}</div>
          <div onClick={save} style={saveBtn}>{base ? t("common.save") : t("common.create")}</div>
        </div>
      </div>
    </div>
  );
}

/* ─────────────────────────── shared modal bits ─────────────────────────── */

const inputStyle: React.CSSProperties = { background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" };
const promptStyle: React.CSSProperties = { height: 200, resize: "vertical", border: "1px solid var(--bd2)", borderRadius: 9, outline: "none", background: "var(--bg-deep)", color: "var(--tx2)", fontFamily: "'IBM Plex Mono',monospace", fontSize: 11.5, lineHeight: 1.7, padding: "13px 15px", boxSizing: "border-box" };
const cancelBtn: React.CSSProperties = { font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)", padding: "8px 16px", border: "1px solid var(--bd2)", borderRadius: 8, cursor: "pointer" };
const saveBtn: React.CSSProperties = { font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 18px", borderRadius: 8, cursor: "pointer" };

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 5, flex: 1 }}>
      <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{label}</span>
      {children}
    </div>
  );
}

function chipStyle(active: boolean): React.CSSProperties {
  return { font: "500 10.5px 'IBM Plex Sans'", color: active ? "var(--tx)" : "var(--tx3)", background: active ? "var(--tint-active)" : "var(--bg-card2)", border: `1px solid ${active ? "var(--tint-active-bd)" : "var(--bd2)"}`, padding: "6px 12px", borderRadius: 7, cursor: "pointer" };
}

function patternBtn(active: boolean, color: string): React.CSSProperties {
  return { flex: 1, textAlign: "center", font: "600 11px 'IBM Plex Sans'", color: active ? color : "var(--tx-dim)", background: active ? "var(--bg-card2)" : "var(--bg-deep)", border: `1px solid ${active ? color : "var(--bd2)"}`, padding: "9px 12px", borderRadius: 8, cursor: "pointer" };
}

function TabSwitch({ tab, setTab }: { tab: "edit" | "example"; setTab: (v: "edit" | "example") => void }) {
  const { t } = useTranslation();
  const s = (active: boolean): React.CSSProperties => ({ font: "500 9.5px 'IBM Plex Sans'", color: active ? "var(--tx)" : "var(--tx-dim)", background: active ? "var(--bg-tab)" : "transparent", padding: "4px 10px", borderRadius: 5, cursor: "pointer" });
  return (
    <div style={{ display: "flex", gap: 3, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: 2 }}>
      <div onClick={() => setTab("edit")} style={s(tab === "edit")}>{t("common.edit")}</div>
      <div onClick={() => setTab("example")} style={s(tab === "example")}>{t("settings.agents.exampleTab")}</div>
    </div>
  );
}

function ExampleBlock({ text, note, onInsert }: { text: string; note: string; onInsert: () => void }) {
  const { t } = useTranslation();
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "9px 12px" }}>
        <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="#67c9a4" strokeWidth="1.5"><path d="M5 8l2 2 4-5" /></svg>
        <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx2)", lineHeight: 1.5 }}>{note}</span>
        <div style={{ flex: 1 }} />
        <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.charCount", { count: text.length })}</span>
        <div onClick={onInsert} style={{ font: "600 10px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "6px 12px", borderRadius: 7, cursor: "pointer" }}>{t("settings.agents.insertExample")}</div>
      </div>
      <pre style={{ margin: 0, background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 9, padding: "14px 16px", fontFamily: "'IBM Plex Mono',monospace", fontSize: 11, lineHeight: 1.7, color: "var(--tx-dim)", whiteSpace: "pre-wrap", maxHeight: 240, overflowY: "auto" }}>{text}</pre>
    </div>
  );
}

/* ─────────────────────────── granularity views ─────────────────────────── */

function SoloView({ onEdit }: { onEdit: (s: SoloAgent | null) => void }) {
  const { t } = useTranslation();
  const solos = useStore((s) => s.solos);
  const globalPrompt = useStore((s) => s.globalPrompt);
  const setGlobalPrompt = useStore((s) => s.setGlobalPrompt);
  const [draft, setDraft] = useState<string | null>(null);
  const value = draft ?? globalPrompt;
  const dirty = draft !== null && draft !== globalPrompt;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 13 }}>
      <RoleNote dot="#8fa3b8" tag={t("settings.agents.soloTag")} tagColor="#8fa3b8" desc={t("settings.agents.soloNote")} />

      <div style={cardStyle}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.agents.registered")}</span>
          <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("settings.agents.agentCount", { count: solos.length })}</span>
          <div onClick={() => onEdit(null)} style={{ marginLeft: "auto", font: "500 11px 'IBM Plex Sans'", color: "var(--ac)", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)", cursor: "pointer" }}>+ {t("settings.agents.newAgent")}</div>
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          {solos.map((a) => (
            <div key={a.id} onClick={() => onEdit(a)} style={{ display: "flex", alignItems: "center", gap: 10, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 8, padding: "10px 13px", cursor: "pointer" }}>
              <div style={{ width: 9, height: 9, borderRadius: "50%", background: a.dot, flex: "none" }} />
              <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)", width: 84 }}>{a.name}</span>
              {/* A command atom has no provider or model to show; what matters
                  about it is the image it runs in and what it runs. */}
              <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)", flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {a.kind === "command"
                  ? `${a.role} · ${a.image || "base"} · ${a.cmd ?? ""}`
                  : `${a.role} · ${a.providerId}/${a.model} · ctx ${a.ctx}`}
              </span>
              <span style={{ font: "500 9px 'IBM Plex Mono'", color: a.kind === "command" ? "#d39a4e" : "#5b9fe8", background: a.kind === "command" ? "var(--tint-amber)" : "var(--tint-blue)", border: `1px solid ${a.kind === "command" ? "var(--bd2)" : "var(--tint-blue-bd)"}`, padding: "2px 7px", borderRadius: 5 }}>
                {a.kind === "command" ? "command" : a.strat}
              </span>
            </div>
          ))}
          {solos.length === 0 && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.agents.noAgents")}</span>}
        </div>
      </div>

      {/* global constitution */}
      <div style={{ ...cardStyle, gap: 12 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <div style={{ width: 9, height: 9, borderRadius: "50%", flex: "none", background: "#8fa3b8" }} />
          <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.agents.globalPrompt")}</span>
          <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("settings.agents.globalPromptHint")}</span>
          <div style={{ flex: 1 }} />
          {dirty && <div onClick={() => { setGlobalPrompt(value); setDraft(null); }} style={{ font: "600 9px 'IBM Plex Mono'", color: "#06121e", background: "var(--ac)", cursor: "pointer", padding: "3px 10px", borderRadius: 6 }}>{t("common.save")}</div>}
          <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: dirty ? "#d39a4e" : "var(--tx-faint)" }}>{t(dirty ? "settings.agents.unsaved" : "settings.agents.saved")}</span>
        </div>
        <textarea value={value} onChange={(e) => setDraft(e.target.value)} spellCheck={false} style={{ ...promptStyle, height: 150 }} />
      </div>
    </div>
  );
}

function StaticView({ onEdit }: { onEdit: (tpl: StaticTemplate | null) => void }) {
  const { t } = useTranslation();
  const staticTpls = useStore((s) => s.staticTpls);
  const solos = useStore((s) => s.solos);
  const soloById = (id: string) => solos.find((s) => s.id === id);
  const tplById = (id: string) => staticTpls.find((x) => x.id === id);
  const [selId, setSelId] = useState(staticTpls[0]?.id ?? "");
  const sel = staticTpls.find((x) => x.id === selId) ?? staticTpls[0];
  const isGraph = sel?.pattern === "graph";
  const note = isGraph
    ? { dot: "#4f9dff", tag: t("settings.agents.graphTag"), color: "#5b9fe8", desc: t("settings.agents.graphNote") }
    : { dot: "#b08ad9", tag: t("settings.agents.supTag"), color: "#c79ae0", desc: t("settings.agents.supNote") };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 13 }}>
      <RoleNote dot={note.dot} tag={note.tag} tagColor={note.color} desc={note.desc} />

      <div style={{ display: "flex", gap: 14, alignItems: "flex-start" }}>
        <div style={{ width: 240, flex: "none", display: "flex", flexDirection: "column", gap: 9 }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "0 2px" }}>
            <span style={{ font: "600 11px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px" }}>TEMPLATES</span>
            <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{staticTpls.length}</span>
          </div>
          {staticTpls.map((tpl) => {
            const active = tpl.id === selId;
            const g = tpl.pattern === "graph";
            return (
              <div key={tpl.id} onClick={() => setSelId(tpl.id)} style={{ display: "flex", alignItems: "center", gap: 8, background: active ? "var(--tint-active)" : "var(--bg-card)", border: `1px solid ${active ? "var(--tint-active-bd)" : "var(--bd)"}`, borderRadius: 9, padding: "10px 12px", cursor: "pointer" }}>
                <div style={{ display: "flex", flexDirection: "column", gap: 4, minWidth: 0, flex: 1 }}>
                  <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{tpl.name}</span>
                  <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{g ? `${tpl.nodes.length} nodes` : `supervisor + ${tpl.workers.length}`}</span>
                </div>
                <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: g ? "#5b9fe8" : "#c79ae0", background: g ? "var(--tint-blue)" : "var(--tint-purple)", padding: "2px 7px", borderRadius: 5 }}>{g ? "graph" : "supervisor"}</span>
              </div>
            );
          })}
          <div onClick={() => onEdit(null)} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--ac)", padding: "8px 11px", border: "1px dashed var(--bd2)", borderRadius: 8, cursor: "pointer", textAlign: "center" }}>+ {t("settings.agents.newTemplate")}</div>
        </div>

        <div style={{ flex: 1, minWidth: 0, ...cardStyle, gap: 15 }}>
          {!sel ? (
            <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{t("settings.agents.noTemplates")}</span>
          ) : (
            <>
              <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                <span style={{ font: "700 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{sel.name}</span>
                <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: isGraph ? "#5b9fe8" : "#c79ae0", background: isGraph ? "var(--tint-blue)" : "var(--tint-purple)", padding: "2px 8px", borderRadius: 5 }}>{isGraph ? "Graph · DAG" : "Supervisor"}</span>
                <div style={{ flex: 1 }} />
                <div onClick={() => onEdit(sel)} style={{ display: "flex", alignItems: "center", gap: 6, font: "600 10px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)" }}>
                  <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="var(--ac)" strokeWidth="1.5"><path d="M11 2.5l2.5 2.5L6 12.5 3 13l.5-3z" /></svg>{t("common.edit")}
                </div>
              </div>
              <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.5, marginTop: -6 }}>{sel.desc}</span>

              {sel.pattern === "graph" ? (
                <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
                  {sel.nodes.map((n) => {
                    const s = n.soloId ? soloById(n.soloId) : undefined;
                    const nt = n.templateId ? tplById(n.templateId) : undefined;
                    const deps = sel.edges.filter(([, to]) => to === n.id).map(([f]) => f);
                    return (
                      <div key={n.id} style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 9, padding: "9px 12px" }}>
                        <div style={{ width: 8, height: 8, borderRadius: "50%", background: nt ? "#b08ad9" : s?.dot ?? "var(--tx-faint)" }} />
                        <span style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{nt?.name ?? s?.name ?? n.soloId ?? n.templateId}</span>
                        <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{nt ? `template (${nt.pattern})` : s?.model}</span>
                        <div style={{ flex: 1 }} />
                        <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{deps.length ? `← ${deps.join(", ")}` : t("settings.agents.root")}</span>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 4 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--tint-purple)", border: "1px solid #6d5499", borderRadius: 9, padding: "11px 16px" }}>
                    <div style={{ width: 9, height: 9, borderRadius: "50%", background: soloById(sel.supervisor)?.dot ?? "var(--tx-faint)" }} />
                    <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{soloById(sel.supervisor)?.name ?? sel.supervisor}</span>
                    <span style={{ font: "400 9px 'IBM Plex Mono'", color: "#c79ae0" }}>supervisor</span>
                  </div>
                  <svg width="14" height="18" viewBox="0 0 14 18" fill="none" stroke="#b08ad9" strokeWidth="1.5"><path d="M7 1v14M7 15l-3-3M7 15l3-3" /></svg>
                  <div style={{ display: "flex", gap: 10, width: "100%" }}>
                    {sel.workers.map((id) => {
                      const s = soloById(id);
                      return (
                        <div key={id} style={{ flex: 1, display: "flex", alignItems: "center", gap: 7, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 9, padding: "11px 13px" }}>
                          <div style={{ width: 8, height: 8, borderRadius: "50%", background: s?.dot ?? "var(--tx-faint)" }} />
                          <span style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{s?.name ?? id}</span>
                          <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>worker</span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function DynamicView() {
  const { t } = useTranslation();
  const solos = useStore((s) => s.solos);
  const staticTpls = useStore((s) => s.staticTpls);
  const dynamicPrompt = useStore((s) => s.dynamicPrompt);
  const setDynamicPrompt = useStore((s) => s.setDynamicPrompt);
  const [draft, setDraft] = useState<string | null>(null);
  const value = draft ?? dynamicPrompt;
  const dirty = draft !== null && draft !== dynamicPrompt;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 13 }}>
      <RoleNote dot="#e0a83e" tag="meta-orchestrator" tagColor="#e0a83e" desc={t("settings.agents.dynamicNote")} />

      <div style={{ ...cardStyle, gap: 16 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.agents.availableSolos")}</span>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 7 }}>
            {solos.map((a) => (
              <div key={a.id} style={{ display: "flex", alignItems: "center", gap: 6, font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx3)", background: "var(--bg-inset2)", border: "1px solid var(--bd3)", padding: "5px 10px", borderRadius: 7 }}>
                <div style={{ width: 7, height: 7, borderRadius: "50%", background: a.dot }} />{a.name}
              </div>
            ))}
          </div>
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.agents.availableTemplates")}</span>
          <div style={{ display: "flex", flexWrap: "wrap", gap: 7 }}>
            {staticTpls.map((tpl) => (
              <div key={tpl.id} style={{ display: "flex", alignItems: "center", gap: 6, font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx3)", background: "var(--bg-inset2)", border: "1px solid var(--bd3)", padding: "5px 10px", borderRadius: 7 }}>
                <div style={{ width: 7, height: 7, borderRadius: "50%", background: tpl.pattern === "graph" ? "#4f9dff" : "#b08ad9" }} />{tpl.name}
              </div>
            ))}
          </div>
        </div>
      </div>

      <div style={{ ...cardStyle, gap: 11 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
          <div style={{ width: 9, height: 9, borderRadius: "50%", flex: "none", background: "#e0a83e" }} />
          <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.agents.systemPrompt")}</span>
          <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>meta-orchestrator</span>
          <div style={{ flex: 1 }} />
          {dirty && <div onClick={() => { setDynamicPrompt(value); setDraft(null); }} style={{ font: "600 9px 'IBM Plex Mono'", color: "#06121e", background: "var(--ac)", cursor: "pointer", padding: "3px 10px", borderRadius: 6 }}>{t("common.save")}</div>}
          <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: dirty ? "#d39a4e" : "var(--tx-faint)" }}>{t(dirty ? "settings.agents.unsaved" : "settings.agents.saved")}</span>
        </div>
        <textarea value={value} onChange={(e) => setDraft(e.target.value)} spellCheck={false} style={{ ...promptStyle, height: 184 }} />
      </div>
    </div>
  );
}

/* ─────────────────────────── panel ─────────────────────────── */

function granBtn(active: boolean): React.CSSProperties {
  return { font: active ? "600 11.5px 'IBM Plex Sans'" : "500 11.5px 'IBM Plex Sans'", color: active ? "var(--tx)" : "var(--tx-dim)", background: active ? "var(--bg-tab)" : "var(--bg-card2)", border: `1px solid ${active ? "var(--bd2)" : "var(--bd3)"}`, padding: "7px 13px", borderRadius: 8, cursor: "pointer" };
}

export function AgentsPanel() {
  const { t } = useTranslation();
  const [gran, setGran] = useState<"solo" | "static" | "dynamic">("solo");
  const [agentModal, setAgentModal] = useState<{ open: boolean; base: SoloAgent | null }>({ open: false, base: null });
  const [staticModal, setStaticModal] = useState<{ open: boolean; base: StaticTemplate | null }>({ open: false, base: null });

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
      {sectionTitle(t("settings.nav.agents"), t("settings.agents.desc"))}

      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
        <div onClick={() => setGran("solo")} style={granBtn(gran === "solo")}>① Solo Agent</div>
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5"><path d="M6 4l4 4-4 4" /></svg>
        <div onClick={() => setGran("static")} style={granBtn(gran === "static")}>② Static Multi-Agent</div>
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5"><path d="M6 4l4 4-4 4" /></svg>
        <div onClick={() => setGran("dynamic")} style={granBtn(gran === "dynamic")}>③ Dynamic Orchestration</div>
      </div>

      {gran === "solo" && <SoloView onEdit={(base) => setAgentModal({ open: true, base })} />}
      {gran === "static" && <StaticView onEdit={(base) => setStaticModal({ open: true, base })} />}
      {gran === "dynamic" && <DynamicView />}

      {agentModal.open && <AgentEditModal base={agentModal.base} onClose={() => setAgentModal({ open: false, base: null })} />}
      {staticModal.open && <StaticEditModal base={staticModal.base} onClose={() => setStaticModal({ open: false, base: null })} />}
    </div>
  );
}
