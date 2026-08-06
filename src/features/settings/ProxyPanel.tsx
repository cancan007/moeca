import { useState } from "react";
import { useTranslation } from "react-i18next";
import { sectionTitle } from "./ui";
import { useStore } from "@/store/useStore";
import { isDesktop, type ProviderInput } from "@/lib/providers";

/* ─── helpers ─── */

const dialectInject: Record<string, Record<string, string>> = {
  anthropic: { "x-api-key": "${SECRET}", "anthropic-version": "2023-06-01" },
  openai: { Authorization: "Bearer ${SECRET}" },
  gemini: { "x-goog-api-key": "${SECRET}" },
};

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
const csv = (a: string[]) => a.join(", ");
const parseCsv = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

const input: React.CSSProperties = { background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none", width: "100%", boxSizing: "border-box" };
const mono: React.CSSProperties = { ...input, fontFamily: "'IBM Plex Mono',monospace", fontSize: 11 };

/* ─── provider edit modal ─── */

function ProviderModal({ base, onClose }: { base: ProviderInput | null; onClose: () => void }) {
  const { t } = useTranslation();
  const upsert = useStore((s) => s.upsertProvider);
  const [name, setName] = useState(base?.name ?? "");
  const [kind, setKind] = useState(base?.kind ?? "model");
  const [dialect, setDialect] = useState(base?.dialect ?? "anthropic");
  const [prefix, setPrefix] = useState(base?.prefix ?? "");
  const [upstream, setUpstream] = useState(base?.upstream ?? "");
  const [allowlist, setAllowlist] = useState(csv(base?.allowlist ?? []));
  const [models, setModels] = useState(csv(base?.models ?? []));
  const [headers, setHeaders] = useState(headersToText(base?.injectHeaders ?? {}));
  const [budget, setBudget] = useState<string>(base?.maxTokensPerSession != null ? String(base.maxTokensPerSession) : "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // For a new model provider, prefill inject headers from the chosen dialect.
  const onDialect = (d: string) => {
    setDialect(d);
    if (!base && dialectInject[d]) setHeaders(headersToText(dialectInject[d]));
  };

  const save = async () => {
    setBusy(true); setErr(null);
    try {
      const bt = budget.trim() === "" ? undefined : parseInt(budget, 10);
      const p: ProviderInput = {
        name: name.trim(),
        kind,
        dialect: kind === "model" ? dialect : undefined,
        prefix: prefix.trim() || `/${name.trim()}/`,
        upstream: upstream.trim(),
        allowlist: parseCsv(allowlist),
        models: parseCsv(models),
        injectHeaders: textToHeaders(headers),
        // Omit when blank so the gateway keeps its current budget; 0 => unlimited.
        maxTokensPerSession: bt != null && Number.isFinite(bt) && bt >= 0 ? bt : undefined,
      };
      if (!p.name) throw new Error(t("settings.proxy.nameRequired"));
      await upsert(p);
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
      setBusy(false);
    }
  };

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", zIndex: 60, display: "flex", alignItems: "center", justifyContent: "center", padding: 30 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 720, maxWidth: "100%", maxHeight: "100%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, display: "flex", flexDirection: "column", overflow: "hidden" }}>
        <div style={{ padding: "15px 20px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "700 15px 'IBM Plex Sans'", color: "var(--tx)" }}>{base ? t("settings.proxy.editTitle", { name: base.name }) : t("settings.proxy.addTitle")}</span>
          <div style={{ flex: 1 }} />
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 19px 'IBM Plex Sans'" }}>✕</div>
        </div>
        <div style={{ flex: 1, overflowY: "auto", padding: "18px 22px", display: "flex", flexDirection: "column", gap: 13 }}>
          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
            <Lbl label={t("settings.proxy.fieldName")}><input value={name} onChange={(e) => setName(e.target.value)} placeholder="openai" style={mono} disabled={!!base} /></Lbl>
            <Lbl label={t("settings.proxy.fieldKind")}>
              <select value={kind} onChange={(e) => setKind(e.target.value)} style={{ ...mono, colorScheme: "dark", cursor: "pointer" }}>
                <option value="model">{t("settings.proxy.kindModel")}</option>
                <option value="tool">{t("settings.proxy.kindTool")}</option>
              </select>
            </Lbl>
            {kind === "model" && (
              <Lbl label={t("settings.proxy.fieldDialect")}>
                <select value={dialect} onChange={(e) => onDialect(e.target.value)} style={{ ...mono, colorScheme: "dark", cursor: "pointer" }}>
                  <option value="anthropic">anthropic</option>
                  <option value="openai">openai</option>
                  <option value="gemini">gemini</option>
                </select>
              </Lbl>
            )}
            <Lbl label={t("settings.proxy.fieldPrefix")}><input value={prefix} onChange={(e) => setPrefix(e.target.value)} placeholder="/openai/" style={mono} /></Lbl>
            <Lbl label={t("settings.proxy.fieldUpstream")}><input value={upstream} onChange={(e) => setUpstream(e.target.value)} placeholder="https://api.openai.com" style={mono} /></Lbl>
            <Lbl label={t("settings.proxy.fieldAllowlist")}><input value={allowlist} onChange={(e) => setAllowlist(e.target.value)} placeholder="api.openai.com" style={mono} /></Lbl>
          </div>
          <Lbl label={t("settings.proxy.fieldModels")}><input value={models} onChange={(e) => setModels(e.target.value)} placeholder="gpt-4o, o3" style={mono} /></Lbl>
          {kind === "model" && (
            <Lbl label={t("settings.proxy.fieldBudget")}>
              <input value={budget} onChange={(e) => setBudget(e.target.value)} placeholder={t("settings.proxy.budgetPlaceholder")} inputMode="numeric" style={mono} />
            </Lbl>
          )}
          <Lbl label={t("settings.proxy.fieldHeaders")}>
            <textarea value={headers} onChange={(e) => setHeaders(e.target.value)} spellCheck={false} style={{ ...mono, height: 78, resize: "vertical", lineHeight: 1.6 }} />
          </Lbl>
          <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.proxy.keyNote")}</span>
          {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</div>}
        </div>
        <div style={{ padding: "13px 20px", borderTop: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 10, justifyContent: "flex-end" }}>
          <div onClick={onClose} style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)", padding: "8px 16px", border: "1px solid var(--bd2)", borderRadius: 8, cursor: "pointer" }}>{t("common.cancel")}</div>
          <div onClick={() => !busy && save()} style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 18px", borderRadius: 8, cursor: "pointer" }}>{busy ? t("settings.proxy.saving") : t("common.save")}</div>
        </div>
      </div>
    </div>
  );
}

function Lbl({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
      <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{label}</span>
      {children}
    </div>
  );
}

/* ─── key (write-only) modal ─── */

function KeyModal({ name, onClose }: { name: string; onClose: () => void }) {
  const { t } = useTranslation();
  const setSecret = useStore((s) => s.setProviderSecret);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const save = async () => {
    setBusy(true); setErr(null);
    try { await setSecret(name, value); onClose(); }
    catch (e) { setErr(e instanceof Error ? e.message : String(e)); setBusy(false); }
  };
  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.55)", zIndex: 60, display: "flex", alignItems: "center", justifyContent: "center", padding: 30 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 460, background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 14, padding: 20, display: "flex", flexDirection: "column", gap: 12 }}>
        <span style={{ font: "700 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.proxy.keyTitle", { name })}</span>
        <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.proxy.keyHint")}</span>
        <input type="password" value={value} onChange={(e) => setValue(e.target.value)} placeholder="sk-…" autoFocus style={mono} />
        {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</div>}
        <div style={{ display: "flex", gap: 10, justifyContent: "flex-end" }}>
          <div onClick={onClose} style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)", padding: "8px 16px", border: "1px solid var(--bd2)", borderRadius: 8, cursor: "pointer" }}>{t("common.cancel")}</div>
          <div onClick={() => !busy && value && save()} style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: value ? "var(--ac)" : "var(--bg-card2)", padding: "8px 18px", borderRadius: 8, cursor: value ? "pointer" : "not-allowed" }}>{busy ? t("settings.proxy.saving") : t("common.save")}</div>
        </div>
      </div>
    </div>
  );
}

/* ─── panel ─── */

export function ProxyPanel() {
  const { t } = useTranslation();
  const providers = useStore((s) => s.providers);
  const views = useStore((s) => s.providerViews);
  const providerError = useStore((s) => s.providerError);
  const deleteProvider = useStore((s) => s.deleteProvider);
  const [edit, setEdit] = useState<{ open: boolean; base: ProviderInput | null }>({ open: false, base: null });
  const [keyFor, setKeyFor] = useState<string | null>(null);
  const desktop = isDesktop();
  const hasSecret = (n: string) => views.find((v) => v.name === n)?.hasSecret ?? false;

  return (
    // Unpositioned root, for the reason spelled out in ToolsPanel: the two
    // modals below are `absolute; inset: 0` and must size to the settings pane,
    // not to however tall this panel's provider list happens to be.
    <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
      {sectionTitle(t("settings.proxy.title"), t("settings.proxy.desc"))}

      {!desktop && (
        <div style={{ font: "400 11px 'IBM Plex Sans'", color: "#d39a4e", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "10px 13px" }}>
          {t("settings.proxy.desktopOnly")}
        </div>
      )}
      {providerError && (
        <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "9px 12px" }}>{providerError}</div>
      )}

      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.proxy.providers")}</span>
        <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{providers.length}</span>
        <div onClick={() => desktop && setEdit({ open: true, base: null })} style={{ marginLeft: "auto", font: "500 11px 'IBM Plex Sans'", color: desktop ? "var(--ac)" : "var(--tx-faint)", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)", cursor: desktop ? "pointer" : "not-allowed" }}>+ {t("settings.proxy.addProvider")}</div>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 9 }}>
        {providers.map((p) => {
          const secret = hasSecret(p.name);
          return (
            <div key={p.name} style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "13px 15px", display: "flex", flexDirection: "column", gap: 9 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
                <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{p.name}</span>
                <span style={{ font: "500 8.5px 'IBM Plex Mono'", color: p.kind === "model" ? "#5b9fe8" : "#e0a83e", background: p.kind === "model" ? "var(--tint-blue)" : "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 7px", borderRadius: 5 }}>{p.kind}{p.kind === "model" && p.dialect ? ` · ${p.dialect}` : ""}</span>
                <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{p.prefix} → {p.upstream || "(dynamic)"}</span>
                <div style={{ flex: 1 }} />
                {desktop && <div onClick={() => setKeyFor(p.name)} style={btn}>{t("settings.proxy.setKey")}</div>}
                {desktop && <div onClick={() => { const v = views.find((x) => x.name === p.name); setEdit({ open: true, base: v ? { ...p, maxTokensPerSession: v.maxTokensPerSession } : p }); }} style={btn}>{t("common.edit")}</div>}
                {desktop && <div onClick={() => deleteProvider(p.name)} style={{ ...btn, color: "var(--red)" }}>{t("common.delete")}</div>}
              </div>
              <div style={{ display: "flex", alignItems: "center", gap: 7, flexWrap: "wrap" }}>
                {p.models.map((m) => <span key={m} style={{ font: "500 9px 'IBM Plex Mono'", color: "var(--tx3)", background: "var(--bg-inset2)", border: "1px solid var(--bd3)", padding: "2px 7px", borderRadius: 5 }}>{m}</span>)}
                <div style={{ flex: 1 }} />
                <span style={{ font: "500 9px 'IBM Plex Mono'", color: secret ? "#67c9a4" : "var(--tx-faint)", background: secret ? "var(--tint-green)" : "var(--bg-card2)", border: `1px solid ${secret ? "var(--tint-green-bd)" : "var(--bd2)"}`, padding: "2px 8px", borderRadius: 5 }}>
                  {t(secret ? "settings.proxy.keySet" : "settings.proxy.keyUnset")}
                </span>
              </div>
            </div>
          );
        })}
      </div>

      {edit.open && <ProviderModal base={edit.base} onClose={() => setEdit({ open: false, base: null })} />}
      {keyFor && <KeyModal name={keyFor} onClose={() => setKeyFor(null)} />}
    </div>
  );
}

const btn: React.CSSProperties = { font: "500 10px 'IBM Plex Sans'", color: "var(--tx3)", cursor: "pointer", padding: "4px 9px", border: "1px solid var(--bd2)", borderRadius: 6 };
