import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { KnowledgeScope } from "@/lib/schedules";
import type { KnowledgeGraph } from "@/lib/knowledge";
import type { SoloAgent } from "@/lib/templates";
import type { ContextState } from "@/lib/chatContext";
import { ringDash } from "@/lib/chatContext";
import { Avatar, ReplyIcon, pillButton, primaryPill } from "./ui";
import { fmtTokens } from "./steps";

/** One quoted passage waiting in the composer. */
export interface Quote {
  id: string;
  from: string;
  text: string;
  /** Where it came from, e.g. a file name and a marked region. */
  loc?: string;
}

/**
 * The composer: what to say, who says it, and what the context costs.
 *
 * The compaction banner is the part worth explaining. It appears at the operator's
 * threshold and offers a choice, and dismissing it buys exactly one turn — past
 * the auto point the alternative to compacting is a turn that overflows, so at
 * that point Chat compacts without asking and says so afterwards rather than
 * asking permission it cannot honour.
 */
export function ChatComposer({
  agent,
  agentLabel,
  ctx,
  banner,
  compacting,
  strategy,
  quotes,
  scope,
  graph,
  disabled,
  hasAgents,
  sending,
  onSend,
  onRemoveQuote,
  onClearQuotes,
  onOpenPicker,
  onCompact,
  onSnooze,
  onAttach,
  onScope,
  onNewChat,
}: {
  agent: SoloAgent | undefined;
  agentLabel: string;
  ctx: ContextState;
  /** Whether to offer compaction now (suggest, and not snoozed). */
  banner: boolean;
  compacting: boolean;
  strategy: "full" | "recent" | "sum";
  quotes: Quote[];
  scope: KnowledgeScope | undefined;
  graph: KnowledgeGraph | null;
  disabled: boolean;
  /** Whether any agent exists to pick. Disabled-with-agents means "choose one";
   *  disabled-without means "go and register one", and telling someone to
   *  register an agent they already have is how a dead end reads as a bug. */
  hasAgents: boolean;
  sending: boolean;
  onSend: (text: string) => void;
  onRemoveQuote: (id: string) => void;
  onClearQuotes: () => void;
  onOpenPicker: () => void;
  onCompact: () => void;
  onSnooze: () => void;
  onAttach: (file: File) => void;
  onScope: (s: KnowledgeScope | undefined) => void;
  onNewChat: () => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState("");
  const [plusOpen, setPlusOpen] = useState(false);
  const [scopeOpen, setScopeOpen] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const send = () => {
    if (disabled || sending || !draft.trim()) return;
    onSend(draft);
    setDraft("");
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      send();
    }
  };

  const pct = Math.round(ctx.ratio * 100);
  const canSend = !disabled && !sending && draft.trim().length > 0;

  const scopeValue = scope ? `${scope.kind}:${scope.id ?? ""}` : "";
  const scopeLabel = !scope
    ? t("daily.scopeUnset")
    : scope.kind === "global"
      ? t("daily.scopeGlobal")
      : (graph?.projects.find((p) => p.id === scope.id)?.name
        ?? graph?.orgs.find((o) => o.id === scope.id)?.name
        ?? scope.id
        ?? "");

  const plusItems = [
    {
      key: "attach",
      icon: "M9.5 2.5H4.5a1 1 0 0 0-1 1v9a1 1 0 0 0 1 1h7a1 1 0 0 0 1-1V5.5zM9.5 2.5V5.5h3",
      label: t("chat.plus.attach"),
      sub: t("chat.plus.attachSub"),
      onClick: () => { setPlusOpen(false); fileRef.current?.click(); },
    },
    {
      key: "scope",
      icon: "M8 1.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13M1.5 8h13M8 1.5c1.6 1.8 2.5 4 2.5 6.5S9.6 12.7 8 14.5C6.4 12.7 5.5 10.5 5.5 8S6.4 3.3 8 1.5",
      label: t("chat.plus.scope"),
      sub: scopeLabel,
      onClick: () => { setPlusOpen(false); setScopeOpen(true); },
    },
    {
      key: "compact",
      icon: "M4 3v10M8 5v6M12 7v2",
      label: t("chat.plus.compact"),
      sub: strategy === "full"
        ? t("chat.plus.compactOff")
        : t("chat.plus.compactSub", { used: fmtTokens(ctx.used), budget: fmtTokens(ctx.budget) }),
      onClick: () => { setPlusOpen(false); if (strategy !== "full") onCompact(); },
    },
    {
      key: "new",
      icon: "M8 3v10M3 8h10",
      label: t("chat.plus.newChat"),
      sub: t("chat.plus.newChatSub"),
      onClick: () => { setPlusOpen(false); onNewChat(); },
    },
  ];

  return (
    <div style={{ flex: "none", display: "flex", justifyContent: "center", padding: "0 32px 20px" }}>
      <div style={{ width: "100%", maxWidth: 740, display: "flex", flexDirection: "column", gap: 9 }}>
        {banner && (
          <div style={{ animation: "ocRise .22s cubic-bezier(.2,.8,.2,1) both", display: "flex", flexWrap: "wrap", alignItems: "center", gap: "10px 12px", background: "var(--bg-panel)", borderRadius: 14, padding: "10px 12px 10px 14px" }}>
            <div style={{ position: "relative", width: 26, height: 26, flex: "none" }}>
              <svg width="26" height="26" viewBox="0 0 26 26">
                <circle cx="13" cy="13" r="10.5" fill="none" stroke="var(--bd)" strokeWidth="3" />
                <circle
                  cx="13" cy="13" r="10.5" fill="none"
                  stroke={ctx.auto ? "var(--red)" : "var(--tx-dim)"}
                  strokeWidth="3" strokeLinecap="round"
                  strokeDasharray={ringDash(ctx.ratio)}
                  transform="rotate(-90 13 13)"
                />
              </svg>
            </div>
            <div style={{ flex: "1 1 180px", minWidth: 150, display: "flex", flexDirection: "column", gap: 2 }}>
              <span style={{ font: "500 12px 'IBM Plex Sans'", color: "var(--tx2)" }}>
                {t("chat.compact.title", { pct })}
              </span>
              <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.55 }}>
                {t(strategy === "recent" ? "chat.compact.subRecent" : "chat.compact.subSum", {
                  count: ctx.replaceable,
                  used: fmtTokens(ctx.used),
                  budget: fmtTokens(ctx.budget),
                })}
              </span>
            </div>
            <div style={{ display: "flex", alignItems: "center", gap: 4, marginLeft: "auto", flex: "none" }}>
              <div onClick={onSnooze} style={pillButton}>{t("chat.compact.later")}</div>
              <div onClick={onCompact} style={primaryPill}>
                {compacting ? t("chat.compact.running") : t("chat.compact.run")}
              </div>
            </div>
          </div>
        )}

        <div style={{ position: "relative", background: "var(--bg-card)", borderRadius: 22, display: "flex", flexDirection: "column", boxShadow: "0 10px 30px rgba(0,0,0,.28)" }}>
          {plusOpen && (
            <>
              <div onClick={() => setPlusOpen(false)} style={{ position: "fixed", inset: 0, zIndex: 30 }} />
              <div style={{ animation: "ocPop .16s cubic-bezier(.2,.8,.2,1) both", position: "absolute", left: 8, bottom: "calc(100% + 8px)", zIndex: 31, width: 310, maxHeight: "min(420px, calc(100vh - 200px))", overflowY: "auto", background: "var(--bg-card2)", borderRadius: 14, boxShadow: "0 16px 44px rgba(0,0,0,.45)", padding: 6, display: "flex", flexDirection: "column", gap: 1 }}>
                {plusItems.map((it) => (
                  <div key={it.key} onClick={it.onClick} style={{ display: "flex", alignItems: "center", gap: 11, padding: "9px 10px", borderRadius: 9, cursor: "pointer" }}>
                    <div style={{ width: 18, height: 18, flex: "none", display: "flex", alignItems: "center", justifyContent: "center", color: "var(--tx3)" }}>
                      <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
                        <path d={it.icon} />
                      </svg>
                    </div>
                    <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 1 }}>
                      <span style={{ font: "500 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{it.label}</span>
                      <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis" }}>{it.sub}</span>
                    </div>
                  </div>
                ))}
              </div>
            </>
          )}

          {scopeOpen && (
            <>
              <div onClick={() => setScopeOpen(false)} style={{ position: "fixed", inset: 0, zIndex: 30 }} />
              <div style={{ animation: "ocPop .16s cubic-bezier(.2,.8,.2,1) both", position: "absolute", left: 8, bottom: "calc(100% + 8px)", zIndex: 31, width: 340, background: "var(--bg-card2)", borderRadius: 14, boxShadow: "0 16px 44px rgba(0,0,0,.45)", padding: 14, display: "flex", flexDirection: "column", gap: 9 }}>
                <span style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx2)" }}>{t("daily.knowledgeScope")}</span>
                <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)", lineHeight: 1.6 }}>{t("chat.scopeHint")}</span>
                {/* The same vocabulary Daily and Delivery use, deliberately: a
                    scope is a node of the graph, and "unset" is not "global" —
                    unset retrieves nothing, global retrieves what is everyone's. */}
                <select
                  value={scopeValue}
                  onChange={(e) => {
                    const v = e.target.value;
                    if (!v) onScope(undefined);
                    else {
                      const [kind, id] = v.split(":");
                      onScope({ kind: kind as KnowledgeScope["kind"], id: id || undefined });
                    }
                    setScopeOpen(false);
                  }}
                  style={{ background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "9px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none", colorScheme: "dark" }}
                >
                  <option value="">{t("daily.scopeUnset")}</option>
                  <option value="global:">{t("daily.scopeGlobal")}</option>
                  {(graph?.orgs ?? []).map((o) => (
                    <optgroup key={o.id} label={o.name}>
                      <option value={`organization:${o.id}`}>{t("daily.scopeWholeOrg", { name: o.name })}</option>
                      {(graph?.projects ?? []).filter((p) => p.orgId === o.id).map((p) => (
                        <option key={p.id} value={`project:${p.id}`}>{p.name}</option>
                      ))}
                    </optgroup>
                  ))}
                </select>
              </div>
            </>
          )}

          {quotes.length > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: 5, margin: "12px 14px 0", maxHeight: "min(168px, 30vh)", overflowY: "auto" }}>
              {quotes.map((q) => (
                <div key={q.id} style={{ display: "flex", alignItems: "flex-start", gap: 8, background: "var(--bg-card2)", borderRadius: 10, padding: "8px 9px" }}>
                  <div style={{ flex: "none", marginTop: 2 }}><ReplyIcon /></div>
                  <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 3 }}>
                    <span style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>
                      {t("chat.quote.to", { from: q.from })}
                      {q.loc && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}> {q.loc}</span>}
                    </span>
                    <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx3)", lineHeight: 1.6, display: "-webkit-box", WebkitLineClamp: 3, WebkitBoxOrient: "vertical", overflow: "hidden" }}>
                      {q.text}
                    </span>
                  </div>
                  <div onClick={() => onRemoveQuote(q.id)} style={{ flex: "none", width: 20, height: 20, borderRadius: "50%", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", color: "var(--tx-dim)", font: "400 11px 'IBM Plex Sans'" }}>✕</div>
                </div>
              ))}
              {quotes.length > 1 && (
                <div style={{ display: "flex", justifyContent: "flex-end" }}>
                  <div onClick={onClearQuotes} style={{ font: "500 10px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "2px 6px", borderRadius: 8, cursor: "pointer" }}>
                    {t("chat.quote.clearAll")}
                  </div>
                </div>
              )}
            </div>
          )}

          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={
              !disabled
                ? t("chat.placeholder")
                : hasAgents
                  ? t("chat.placeholderPickAgent")
                  : t("chat.placeholderNoAgent")
            }
            rows={2}
            style={{ resize: "none", background: "transparent", border: "none", outline: "none", padding: "16px 20px 6px", font: "400 13.5px 'IBM Plex Sans'", color: "var(--tx)", lineHeight: 1.65, fontFamily: "'IBM Plex Sans', sans-serif" }}
          />

          <div style={{ display: "flex", alignItems: "center", gap: 6, padding: "6px 12px 12px", minWidth: 0, overflow: "hidden" }}>
            <div
              onClick={() => setPlusOpen((v) => !v)}
              style={{ width: 28, height: 28, flex: "none", borderRadius: "50%", background: "var(--bg-card2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", color: "var(--tx3)", font: "400 16px 'IBM Plex Sans'", lineHeight: 1 }}
            >
              +
            </div>
            <div
              onClick={onOpenPicker}
              style={{ flex: "none", whiteSpace: "nowrap", cursor: "pointer", display: "flex", alignItems: "center", gap: 7, font: "500 11px 'IBM Plex Sans'", color: "var(--tx2)", borderRadius: 15, padding: "6px 10px 6px 8px" }}
            >
              {agent ? <Avatar agent={agent} size={18} /> : <div style={{ width: 8, height: 8, borderRadius: "50%", background: "var(--bd-sep)" }} />}
              {agentLabel}
              <svg width="10" height="10" viewBox="0 0 16 16" fill="none" stroke="var(--tx-dim)" strokeWidth="1.8"><path d="m4 6 4 4 4-4" /></svg>
            </div>

            <div style={{ flex: 1, minWidth: 0, display: "flex", justifyContent: "flex-end", overflow: "hidden" }}>
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", whiteSpace: "nowrap" }}>{t("chat.sendHint")}</span>
            </div>
            <div
              onClick={send}
              title={t("chat.send")}
              style={{
                width: 30, height: 30, flex: "none", borderRadius: "50%",
                background: canSend ? "var(--ac)" : "var(--bg-card2)",
                color: canSend ? "#06121e" : "var(--tx-faint)",
                display: "flex", alignItems: "center", justifyContent: "center",
                cursor: canSend ? "pointer" : "not-allowed",
              }}
            >
              <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.8">
                <path d="M8 13V3M3.5 7.5 8 3l4.5 4.5" />
              </svg>
            </div>
          </div>
        </div>

        <span style={{ font: "400 9.5px 'IBM Plex Sans'", color: "var(--tx-faint)", textAlign: "center" }}>
          {t("chat.footNote")}
        </span>
      </div>

      <input
        ref={fileRef}
        type="file"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) onAttach(f);
          e.target.value = "";
        }}
        style={{ display: "none" }}
      />
    </div>
  );
}
