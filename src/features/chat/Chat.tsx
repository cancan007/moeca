import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { Artifact } from "@/lib/daily";
import { knowledge as knowledgeApi, type KnowledgeGraph } from "@/lib/knowledge";
import { useStore } from "@/store/useStore";
import { AgentPicker } from "./AgentPicker";
import { ChatComposer, type Quote } from "./ChatComposer";
import { ChatPanel } from "./ChatPanel";
import { ChatRail } from "./ChatRail";
import { ChatThread } from "./ChatThread";
import { PromoteDialog } from "./PromoteDialog";
import { PanelIcon, iconButton, statusColor } from "./ui";
import { useChat } from "./useChat";

/**
 * Chat: a standing conversation with an agent, at the same granularity as
 * Delivery and Daily.
 *
 * It is not a lighter Delivery. A Delivery task is git work reviewed as a diff and
 * merged; a Daily schedule is unattended work reviewed as artifacts. This is the
 * third thing those two never covered — asking, trying something, working a
 * problem out — where the unit is a turn rather than a task, and the whole point is
 * that the next turn knows about the last one.
 *
 * Everything under the surface is the machinery that already existed: a turn
 * compiles the bound template through compileRef and runs it as an orchestrator
 * run, in a sandbox, with the gateway holding the keys and Audit seeing every
 * call. What Chat adds is continuity, and continuity is what has to be paid for —
 * hence the context ring and the compaction banner in the composer.
 */

const PANEL_DEFAULT = 340;
const PANEL_MIN = 260;
const PANEL_MAX = 620;

export function Chat() {
  const { t } = useTranslation();
  const solos = useStore((s) => s.solos);
  const history = useStore((s) => s.history);
  const c = useChat();

  const [railOpen, setRailOpen] = useState(true);
  const [panelOpen, setPanelOpen] = useState(true);
  const [panelWidth, setPanelWidth] = useState(PANEL_DEFAULT);
  const [dragging, setDragging] = useState(false);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [file, setFile] = useState<Artifact | null>(null);
  const [quotes, setQuotes] = useState<Quote[]>([]);
  const [promoteText, setPromoteText] = useState<string | null>(null);
  const [graph, setGraph] = useState<KnowledgeGraph | null>(null);
  const [selection, setSelection] = useState<{ x: number; y: number; text: string } | null>(null);

  const scrollRef = useRef<HTMLDivElement>(null);
  const colRef = useRef<HTMLDivElement>(null);

  // The scope picker names nodes of the Knowledge graph, so it needs the graph.
  // Best-effort: without it the picker still offers "unset" and "global", which
  // are the two choices that do not depend on what the graph contains.
  useEffect(() => {
    if (!c.live) return;
    knowledgeApi.graph().then(setGraph).catch(() => setGraph(null));
  }, [c.live]);

  // Follow the conversation as it grows. Only when already near the bottom, so
  // reading back through a long transcript is not interrupted by the next reply.
  const turnCount = c.turns.length;
  const lastText = c.turns[c.turns.length - 1]?.text ?? "";
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const near = el.scrollHeight - el.scrollTop - el.clientHeight < 240;
    if (near) el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
  }, [turnCount, lastText, c.activeId]);

  /* ── the panel's drag handle ──────────────────────────────────────── */

  const onResizeStart = useCallback((e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    const startW = panelWidth;
    setDragging(true);
    const move = (ev: MouseEvent) => {
      const next = Math.min(PANEL_MAX, Math.max(PANEL_MIN, startW - (ev.clientX - startX)));
      setPanelWidth(next);
    };
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
      document.body.style.cursor = "";
      // Selection stays off for the length of the drag rather than per element:
      // dragging over the transcript would otherwise highlight it, and the
      // selection then opens the reply chip the moment the button comes up.
      document.body.style.userSelect = "";
      setDragging(false);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
  }, [panelWidth]);

  /* ── quoting ──────────────────────────────────────────────────────── */

  const addQuote = useCallback((q: Omit<Quote, "id">) => {
    setQuotes((prev) => [...prev, { ...q, id: `${Date.now()}-${prev.length}` }]);
  }, []);

  /** Selecting text in the transcript offers to reply to exactly that passage.
   *  The button is positioned over the selection rather than docked somewhere,
   *  because the thing being replied to is what the person is looking at. */
  const onSelectionUp = useCallback((e: React.MouseEvent) => {
    const sel = window.getSelection();
    const text = sel?.toString().trim() ?? "";
    if (!text || text.length < 2) { setSelection(null); return; }
    const box = colRef.current?.getBoundingClientRect();
    if (!box) return;
    setSelection({ x: e.clientX - box.left, y: e.clientY - box.top, text });
  }, []);

  const takeSelection = () => {
    if (!selection) return;
    addQuote({ from: t("chat.quote.fromThread"), text: selection.text });
    window.getSelection()?.removeAllRanges();
    setSelection(null);
  };

  /* ── sending ──────────────────────────────────────────────────────── */

  const onSend = async (text: string) => {
    const ok = await c.send(text, quotes.map((q) => (q.loc ? `${q.loc}\n${q.text}` : q.text)));
    if (ok) setQuotes([]);
  };

  const agentLabel = c.agent?.name ?? (solos.length === 0 ? t("chat.noAgents") : t("chat.pickAgent"));
  const headerMeta = c.active
    ? t("chat.header.meta", { agent: c.agent?.name ?? "—", count: c.turns.filter((x) => x.kind !== "summary").length })
    : "";
  const status = c.pending ? "running" : c.turns[c.turns.length - 1]?.status ?? "";

  if (!c.live) {
    return (
      <div style={{ flex: 1, display: "flex", alignItems: "center", justifyContent: "center", padding: 32 }}>
        <div style={{ display: "flex", flexDirection: "column", alignItems: "center", gap: 10, maxWidth: 420, textAlign: "center" }}>
          <span style={{ font: "600 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("errors.hostAgentOffline")}</span>
          <span style={{ font: "400 12px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.7 }}>{t("chat.offlineBody")}</span>
        </div>
      </div>
    );
  }

  return (
    <div style={{ animation: "ocScreenIn .2s cubic-bezier(.2,.8,.2,1) both", flex: 1, display: "flex", minHeight: 0, minWidth: 0, background: "var(--bg-app)", position: "relative" }}>
      {pickerOpen && (
        <AgentPicker
          agents={solos}
          activeId={c.agent?.id}
          onPick={(id) => void c.bindAgent(id)}
          onClose={() => setPickerOpen(false)}
        />
      )}
      {promoteText !== null && (
        <PromoteDialog
          text={promoteText}
          title={c.active?.title ?? ""}
          onClose={() => setPromoteText(null)}
        />
      )}

      <ChatRail
        conversations={c.conversations}
        activeId={c.activeId}
        open={railOpen}
        onToggle={() => setRailOpen((v) => !v)}
        onSelect={(id) => { c.select(id); setFile(null); setQuotes([]); }}
        onNew={() => void c.create()}
        onRemove={(id) => void c.remove(id)}
      />

      <div ref={colRef} style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", minHeight: 0, position: "relative" }}>
        {selection && (
          <div
            onMouseDown={(e) => e.preventDefault()}
            onClick={takeSelection}
            style={{
              position: "absolute", left: selection.x, top: selection.y, zIndex: 20,
              transform: "translate(-50%, calc(-100% - 8px))",
              animation: "ocPopUp .14s cubic-bezier(.2,.8,.2,1) both",
              display: "flex", alignItems: "center", gap: 6, cursor: "pointer",
              background: "var(--bg-card2)", color: "var(--tx2)",
              font: "500 11px 'IBM Plex Sans'", padding: "6px 11px", borderRadius: 14,
              boxShadow: "0 8px 24px rgba(0,0,0,.4)",
            }}
          >
            <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round">
              <path d="M6.5 4 2.5 8l4 4M3 8h6.5a4 4 0 0 1 4 4v.5" />
            </svg>
            {t("chat.actions.reply")}
          </div>
        )}

        <div style={{ height: 56, flex: "none", display: "flex", alignItems: "center", gap: 12, padding: "0 22px", minWidth: 0, overflow: "hidden" }}>
          {!railOpen && (
            <div style={{ display: "flex", alignItems: "center", gap: 2, flex: "none", marginLeft: -8 }}>
              <div onClick={() => setRailOpen(true)} title={t("chat.rail.open")} style={iconButton()}>
                <PanelIcon />
              </div>
              <div onClick={() => void c.create()} title={t("chat.rail.new")} style={{ ...iconButton(), font: "400 17px 'IBM Plex Sans'" }}>+</div>
            </div>
          )}
          <div style={{ flex: 1, minWidth: 60, display: "flex", alignItems: "baseline", gap: 10, overflow: "hidden" }}>
            <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", flex: "0 1 auto", minWidth: 40 }}>
              {c.active ? c.active.title || t("chat.untitled") : t("chat.rail.new")}
            </span>
            <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", flex: "0 100 auto", minWidth: 0 }}>
              {headerMeta}
            </span>
          </div>
          {status && (
            <div style={{ display: "flex", flex: "none", alignItems: "center", gap: 6, font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)", whiteSpace: "nowrap" }}>
              <div
                className={status === "running" ? "oc-active-dot" : undefined}
                style={{ width: 6, height: 6, borderRadius: "50%", background: statusColor(status) }}
              />
              {t(`chat.status.${status}`, { defaultValue: status })}
              {c.pending && (
                <span onClick={() => void c.stop()} style={{ marginLeft: 4, color: "var(--tx3)", cursor: "pointer" }}>
                  {t("chat.stop")}
                </span>
              )}
            </div>
          )}
          <div
            onClick={() => setPanelOpen((v) => !v)}
            title={t("chat.panel.toggle")}
            style={{ ...iconButton(panelOpen), color: panelOpen ? "var(--tx2)" : "var(--tx-dim)" }}
          >
            <PanelIcon mirrored />
          </div>
        </div>

        {c.err && (
          <div style={{ margin: "0 32px 8px", display: "flex", alignItems: "center", gap: 10, background: "var(--tint-red)", border: "1px solid var(--tint-red-bd)", borderRadius: 10, padding: "9px 12px" }}>
            <span style={{ flex: 1, font: "400 11px 'IBM Plex Mono'", color: "var(--red)", lineHeight: 1.6, wordBreak: "break-word" }}>{c.err}</span>
            <div onClick={c.clearErr} style={{ ...iconButton(), width: 20, height: 20, font: "400 11px 'IBM Plex Sans'" }}>✕</div>
          </div>
        )}

        <ChatThread
          turns={c.turns}
          artifacts={c.artifacts}
          agent={c.agent}
          runStatus={c.runStatus}
          stageLogs={c.stageLogs}
          turnRuns={c.turnRuns}
          onLoadTurnRun={(id, runId) => void c.loadTurnRun(id, runId)}
          pendingTurnId={c.pending?.turnId ?? null}
          onQuote={(text, from) => addQuote({ from, text })}
          onOpenFile={(a) => { setPanelOpen(true); setFile(a); }}
          onRegenerate={() => void c.regenerate()}
          onPromote={(text) => setPromoteText(text)}
          scrollRef={scrollRef}
          onSelectionUp={onSelectionUp}
        />

        <ChatComposer
          agent={c.agent}
          agentLabel={agentLabel}
          ctx={c.ctx}
          banner={c.ctx.suggest && !c.snoozed}
          compacting={c.busy === "compact"}
          strategy={history.strategy}
          quotes={quotes}
          scope={c.active?.scope}
          graph={graph}
          disabled={!c.agent}
          hasAgents={solos.length > 0}
          sending={c.busy === "send" || !!c.pending}
          onSend={(text) => void onSend(text)}
          onRemoveQuote={(id) => setQuotes((prev) => prev.filter((q) => q.id !== id))}
          onClearQuotes={() => setQuotes([])}
          onOpenPicker={() => setPickerOpen(true)}
          onCompact={() => void c.compact()}
          onSnooze={c.snooze}
          onAttach={(f) => void c.attach(f)}
          onScope={(s) => void c.setScope(s)}
          onNewChat={() => void c.create()}
        />
      </div>

      <ChatPanel
        conversationId={c.activeId}
        artifacts={c.artifacts}
        open={panelOpen}
        width={panelWidth}
        dragging={dragging}
        file={file}
        onOpenFile={setFile}
        onCloseFile={() => setFile(null)}
        onToggle={() => setPanelOpen(false)}
        onQuote={addQuote}
        onResizeStart={onResizeStart}
        onResizeReset={() => setPanelWidth(PANEL_DEFAULT)}
      />
    </div>
  );
}
