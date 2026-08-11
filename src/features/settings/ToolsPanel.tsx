import { useState } from "react";
import { useTranslation } from "react-i18next";
import { sectionTitle } from "./ui";
import { useStore } from "@/store/useStore";
import type { ToolDef, ToolParam, ToolOutput } from "@/lib/tools";

type OutKind = ToolOutput["kind"];

const input: React.CSSProperties = { background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none", width: "100%", boxSizing: "border-box" };
const mono: React.CSSProperties = { ...input, fontFamily: "'IBM Plex Mono',monospace", fontSize: 11 };

/** The tool-template placeholder syntax, shown verbatim in field labels. */
const TPL = "{{param}}";

function slug(name: string): string {
  return name.trim().toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "") || `tool_${Math.floor(Date.now() % 100000)}`;
}

function Lbl({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
      <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{label}</span>
      {children}
    </div>
  );
}

function headersToText(h: Record<string, string>): string {
  return Object.entries(h).map(([k, v]) => `${k}: ${v}`).join("\n");
}
function textToHeaders(t: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of t.split("\n")) {
    const i = line.indexOf(":");
    if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  return out;
}

function ToolModal({ base, onClose }: { base: ToolDef | null; onClose: () => void }) {
  const { t } = useTranslation();
  const upsert = useStore((s) => s.upsertTool);
  const del = useStore((s) => s.deleteTool);
  const [name, setName] = useState(base?.name ?? "");
  const [description, setDescription] = useState(base?.description ?? "");
  const [method, setMethod] = useState(base?.method ?? "POST");
  const [path, setPath] = useState(base?.path ?? "");
  const [params, setParams] = useState<ToolParam[]>(base?.params ?? []);
  const [headers, setHeaders] = useState(headersToText(base?.headers ?? {}));
  const [body, setBody] = useState(base?.body ?? "");
  const [target, setTarget] = useState(base?.targetHeader ?? "");
  // What the response becomes. A text tool answers the model; an artifact tool
  // writes the bytes into /work and tells the model only where they went —
  // which is the only way a tool can produce an image, since base64 through the
  // model's context is both truncated and undecodable by write_file.
  const [outKind, setOutKind] = useState<OutKind>(base?.output?.kind ?? "text");
  const [jsonPath, setJsonPath] = useState(base?.output?.jsonPath ?? "");
  const [exts, setExts] = useState((base?.output?.extensions ?? []).join(", "));
  const [defaults, setDefaults] = useState(headersToText(base?.defaults ?? {}));
  const [poll, setPoll] = useState(!!base?.output?.poll);
  const [pollDone, setPollDone] = useState((base?.output?.poll?.done ?? ["completed"]).join(", "));
  const [pollFail, setPollFail] = useState((base?.output?.poll?.fail ?? ["failed", "cancelled"]).join(", "));
  const [pollStatusUrl, setPollStatusUrl] = useState(base?.output?.poll?.statusUrl ?? "/{{id}}");
  const [pollResultUrl, setPollResultUrl] = useState(base?.output?.poll?.resultUrl ?? "/{{id}}/content");

  const setParam = (i: number, patch: Partial<ToolParam>) =>
    setParams((ps) => ps.map((p, j) => (j === i ? { ...p, ...patch } : p)));

  const save = () => {
    const id = base?.id ?? slug(name || "tool");
    const list = (v: string) => v.split(",").map((x) => x.trim()).filter(Boolean);
    const defs = textToHeaders(defaults);
    upsert({
      id, name: name.trim() || "tool", description,
      params: params.filter((p) => p.name.trim()),
      method, path: path.trim(), headers: textToHeaders(headers), body, targetHeader: target.trim(),
      ...(Object.keys(defs).length ? { defaults: defs } : {}),
      // Absent for a text tool, so its stored shape is exactly what it was
      // before artifact outputs existed.
      ...(outKind === "text" ? {} : {
        output: {
          kind: outKind,
          ...(outKind === "base64" ? { jsonPath: jsonPath.trim() } : {}),
          ...(list(exts).length ? { extensions: list(exts) } : {}),
          ...(poll ? { poll: { idPath: "id", statusPath: "status", errorPath: "error",
                               done: list(pollDone), fail: list(pollFail),
                               statusUrl: pollStatusUrl.trim(), resultUrl: pollResultUrl.trim() } } : {}),
        },
      }),
    });
    onClose();
  };

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", zIndex: 60, display: "flex", alignItems: "center", justifyContent: "center", padding: 30 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 760, maxWidth: "100%", maxHeight: "100%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, display: "flex", flexDirection: "column", overflow: "hidden" }}>
        <div style={{ padding: "15px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "700 15px 'IBM Plex Sans'", color: "var(--tx)" }}>{base ? t("settings.tools.editTitle", { name: base.name }) : t("settings.tools.addTitle")}</span>
          <div style={{ flex: 1 }} />
          {base && <div onClick={() => { del(base.id); onClose(); }} style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--red)", cursor: "pointer", padding: "4px 10px", border: "1px solid var(--tint-red-bd)", borderRadius: 6 }}>{t("common.delete")}</div>}
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 19px 'IBM Plex Sans'" }}>✕</div>
        </div>
        <div style={{ flex: 1, overflowY: "auto", padding: "18px 22px", display: "flex", flexDirection: "column", gap: 13 }}>
          <Lbl label={t("settings.tools.fieldName")}><input value={name} onChange={(e) => setName(e.target.value)} placeholder="post_slack_message" style={mono} /></Lbl>
          <Lbl label={t("settings.tools.fieldDescription")}><input value={description} onChange={(e) => setDescription(e.target.value)} placeholder={t("settings.tools.descriptionPlaceholder")} style={input} /></Lbl>
          <div style={{ display: "grid", gridTemplateColumns: "120px 1fr", gap: 12 }}>
            <Lbl label={t("settings.tools.fieldMethod")}>
              <select value={method} onChange={(e) => setMethod(e.target.value)} style={{ ...mono, colorScheme: "dark", cursor: "pointer" }}>
                {["POST", "GET", "PUT", "PATCH", "DELETE"].map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
            </Lbl>
            {/* `tpl` is passed in rather than written into the dictionary:
                a literal {{param}} in a translation string would be read as an
                i18next interpolation and eaten. skipOnVariables (on by
                default) stops the injected value being re-scanned. */}
            <Lbl label={t("settings.tools.fieldPath", { tpl: TPL })}><input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/slack/channels/{{channel}}/messages" style={mono} /></Lbl>
          </div>

          <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.tools.fieldParams")}</span>
              <div onClick={() => setParams((p) => [...p, { name: "", type: "string", description: "", required: false }])} style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer" }}>+ {t("common.add")}</div>
            </div>
            {params.map((p, i) => (
              <div key={i} style={{ display: "grid", gridTemplateColumns: "1fr 90px 1fr 60px 24px", gap: 6, alignItems: "center" }}>
                <input value={p.name} onChange={(e) => setParam(i, { name: e.target.value })} placeholder="channel" style={mono} />
                <select value={p.type} onChange={(e) => setParam(i, { type: e.target.value })} style={{ ...mono, colorScheme: "dark", cursor: "pointer" }}>
                  {["string", "number", "boolean"].map((t) => <option key={t} value={t}>{t}</option>)}
                </select>
                <input value={p.description} onChange={(e) => setParam(i, { description: e.target.value })} placeholder={t("settings.tools.paramDescription")} style={input} />
                <label style={{ display: "flex", alignItems: "center", gap: 4, font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                  <input type="checkbox" checked={p.required} onChange={(e) => setParam(i, { required: e.target.checked })} />{t("common.required")}
                </label>
                <div onClick={() => setParams((ps) => ps.filter((_, j) => j !== i))} style={{ cursor: "pointer", color: "var(--tx-faint)", textAlign: "center" }}>×</div>
              </div>
            ))}
          </div>

          <Lbl label={t("settings.tools.fieldHeaders", { tpl: TPL })}><textarea value={headers} onChange={(e) => setHeaders(e.target.value)} spellCheck={false} style={{ ...mono, height: 52, resize: "vertical" }} /></Lbl>
          <Lbl label={t("settings.tools.fieldBody", { tpl: TPL })}><textarea value={body} onChange={(e) => setBody(e.target.value)} spellCheck={false} placeholder={`{"text":"{{text}}"}`} style={{ ...mono, height: 60, resize: "vertical" }} /></Lbl>
          <Lbl label={t("settings.tools.fieldDefaults", { tpl: TPL })}><textarea value={defaults} onChange={(e) => setDefaults(e.target.value)} spellCheck={false} placeholder="voice: alloy" style={{ ...mono, height: 40, resize: "vertical" }} /></Lbl>
          <Lbl label={t("settings.tools.fieldTarget")}><input value={target} onChange={(e) => setTarget(e.target.value)} placeholder="https://{{host}}/…" style={mono} /></Lbl>

          {/* Output binding. This is what lets one mechanism cover both "tell
              the model something" and "make a file" — generation is no longer
              a separate, vendor-shaped feature. */}
          <Lbl label={t("settings.tools.fieldOutput")}>
            <div style={{ display: "flex", gap: 6 }}>
              {(["text", "base64", "binary"] as OutKind[]).map((k) => (
                <div key={k} onClick={() => setOutKind(k)} title={t(`settings.tools.output.${k}.hint`)}
                  style={{ flex: 1, textAlign: "center", cursor: "pointer", font: "500 10.5px 'IBM Plex Mono'", padding: "6px 8px", borderRadius: 7,
                    color: outKind === k ? "#06121e" : "var(--tx3)", background: outKind === k ? "var(--ac)" : "var(--bg-card2)",
                    border: `1px solid ${outKind === k ? "var(--ac)" : "var(--bd2)"}` }}>
                  {t(`settings.tools.output.${k}.label`)}
                </div>
              ))}
            </div>
          </Lbl>
          {outKind !== "text" && (
            <>
              {outKind === "base64" && (
                <Lbl label={t("settings.tools.fieldJsonPath")}>
                  <input value={jsonPath} onChange={(e) => setJsonPath(e.target.value)} placeholder="data.0.b64_json" style={mono} />
                </Lbl>
              )}
              <Lbl label={t("settings.tools.fieldExtensions")}>
                <input value={exts} onChange={(e) => setExts(e.target.value)} placeholder=".png, .jpg, .webp" style={mono} />
              </Lbl>
              <div onClick={() => setPoll((v) => !v)} style={{ display: "flex", alignItems: "center", gap: 7, cursor: "pointer" }}>
                <div style={{ width: 13, height: 13, borderRadius: 4, border: `1px solid ${poll ? "var(--ac)" : "var(--bd2)"}`, background: poll ? "var(--ac)" : "transparent" }} />
                <span style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx3)" }}>{t("settings.tools.pollLabel")}</span>
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.tools.pollHint")}</span>
              </div>
              {poll && (
                <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 8 }}>
                  <Lbl label={t("settings.tools.pollDone")}><input value={pollDone} onChange={(e) => setPollDone(e.target.value)} style={mono} /></Lbl>
                  <Lbl label={t("settings.tools.pollFail")}><input value={pollFail} onChange={(e) => setPollFail(e.target.value)} style={mono} /></Lbl>
                  <Lbl label={t("settings.tools.pollStatusUrl")}><input value={pollStatusUrl} onChange={(e) => setPollStatusUrl(e.target.value)} style={mono} /></Lbl>
                  <Lbl label={t("settings.tools.pollResultUrl")}><input value={pollResultUrl} onChange={(e) => setPollResultUrl(e.target.value)} style={mono} /></Lbl>
                </div>
              )}
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.tools.artifactNote")}</span>
            </>
          )}
          <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.tools.gatewayNote")}</span>
        </div>
        <div style={{ padding: "13px 20px", borderTop: "1px solid var(--bd)", display: "flex", gap: 10, justifyContent: "flex-end" }}>
          <div onClick={onClose} style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)", padding: "8px 16px", border: "1px solid var(--bd2)", borderRadius: 8, cursor: "pointer" }}>{t("common.cancel")}</div>
          <div onClick={save} style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 18px", borderRadius: 8, cursor: "pointer" }}>{t("common.save")}</div>
        </div>
      </div>
    </div>
  );
}

export function ToolsPanel() {
  const { t } = useTranslation();
  const tools = useStore((s) => s.tools);
  const [edit, setEdit] = useState<{ open: boolean; base: ToolDef | null }>({ open: false, base: null });

  return (
    // No `position: relative` here on purpose: the modal below is `absolute;
    // inset: 0`, so a positioned root would size the overlay to THIS panel
    // rather than the settings pane. With no tools registered the panel is a
    // couple of rows tall, which squeezed the "add tool" button down to a
    // sliver. Leaving the root unpositioned anchors it to the settings content
    // area, the same ancestor AgentsPanel's modals already use.
    <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
      {sectionTitle(t("settings.nav.tools"), t("settings.tools.desc"))}

      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.tools.registered")}</span>
        <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{tools.length}</span>
        <div onClick={() => setEdit({ open: true, base: null })} style={{ marginLeft: "auto", font: "500 11px 'IBM Plex Sans'", color: "var(--ac)", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)", cursor: "pointer" }}>+ {t("settings.tools.addTool")}</div>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {tools.length === 0 && <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.tools.none")}</span>}
        {tools.map((tool) => (
          <div key={tool.id} onClick={() => setEdit({ open: true, base: tool })} style={{ display: "flex", alignItems: "center", gap: 10, background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 9, padding: "11px 14px", cursor: "pointer" }}>
            <span style={{ font: "600 12px 'IBM Plex Mono'", color: "var(--tx)" }}>{tool.name}</span>
            <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: "#5b9fe8", background: "var(--tint-blue)", padding: "2px 7px", borderRadius: 5 }}>{tool.method} {tool.path}</span>
            <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{tool.description}</span>
            <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{tool.params.length} params</span>
          </div>
        ))}
      </div>

      {edit.open && <ToolModal base={edit.base} onClose={() => setEdit({ open: false, base: null })} />}
    </div>
  );
}
