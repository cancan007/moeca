import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { chat, type Conversation, type Turn } from "@/lib/chat";
import type { Artifact } from "@/lib/daily";
import { sandbox, type RunStatus } from "@/lib/sandbox";
import { useStore } from "@/store/useStore";
import {
  buildHistory,
  buildTask,
  contextState,
  deriveTitle,
  type ContextState,
} from "@/lib/chatContext";
import {
  compileRef,
  buildRunSpec,
  normalizeRef,
  sinkStageId,
  type TemplateStores,
} from "@/lib/agentTemplates";
import type { SoloAgent } from "@/lib/templates";

/**
 * The state and the lifecycle of one Chat screen.
 *
 * A turn is not a message send, it is a run: compile the bound template, hand the
 * spec to the host, and follow the run until it has said something. The two
 * sources that follow it are deliberately different — the host owns the
 * transcript (so a reply lands even if this window is closed) and the sandbox
 * controller owns per-stage progress (so the collapsible log is live rather than
 * reconstructed afterwards).
 */

const POLL_MS = 2000;

export interface PendingRun {
  turnId: number;
  runId: string;
  agent: string;
}

export function useChat() {
  const { t } = useTranslation();
  const live = useStore((s) => s.source === "live");
  const activeId = useStore((s) => s.chatId);
  const setActiveId = useStore((s) => s.setChatId);
  const addNotif = useStore((s) => s.addNotif);

  const solos = useStore((s) => s.solos);
  const staticTpls = useStore((s) => s.staticTpls);
  const providers = useStore((s) => s.providers);
  const tools = useStore((s) => s.tools);
  const history = useStore((s) => s.history);
  const compactorSoloId = useStore((s) => s.compactorSoloId);
  const setLastCompaction = useStore((s) => s.setLastCompaction);

  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [turns, setTurns] = useState<Turn[]>([]);
  const [artifacts, setArtifacts] = useState<Artifact[]>([]);
  const [pending, setPending] = useState<PendingRun | null>(null);
  const [runStatus, setRunStatus] = useState<RunStatus | null>(null);
  const [stageLogs, setStageLogs] = useState<Record<string, string>>({});
  /** Per-turn run detail, kept so the work log stays readable after the turn
   *  finishes — including for turns from an earlier session, whose logs the
   *  controller still serves from its archive once the containers are gone.
   *  Loaded when someone opens the log rather than for every turn on sight. */
  const [turnRuns, setTurnRuns] = useState<Record<number, { status: RunStatus | null; logs: Record<string, string> }>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  /** Dismissing the compaction banner buys one turn, not the rest of the
   *  conversation: at the auto threshold the alternative to compacting is
   *  failing, so the snooze is cleared on the next send. */
  const [snoozed, setSnoozed] = useState(false);
  /** What the last compaction actually saved, as the host reported it. The
   *  Settings badge used to show a hard-coded −38%. */
  const [lastSaving, setLastSaving] = useState<{ replaced: number; summary: number } | null>(null);

  const timer = useRef<number | null>(null);
  useEffect(() => () => { if (timer.current) window.clearInterval(timer.current); }, []);

  const tplStores: TemplateStores = useMemo(
    () => ({ solos, staticTpls, providers, tools, history }),
    [solos, staticTpls, providers, tools, history],
  );

  const active = conversations.find((c) => c.id === activeId) ?? null;

  /** The agent this conversation talks to. Resolved from the stored ref so a
   *  conversation bound to an agent that has since been deleted degrades to "not
   *  bound" instead of pointing at nothing. */
  const agent: SoloAgent | undefined = useMemo(() => {
    const ref = active?.templateRef ?? "";
    const [kind, id] = ref.split(":");
    const bound = kind === "solo" ? solos.find((s) => s.id === id) : undefined;
    // With no conversation open yet, the agent shown is the one a new
    // conversation will bind. Without this the composer is disabled on a fresh
    // install and the first message can never be typed — the conversation is
    // created BY sending, so waiting for one to exist first is a deadlock.
    return bound ?? (active ? undefined : solos[0]);
  }, [active, solos]);

  const ctx: ContextState = useMemo(() => contextState(turns, agent, history), [turns, agent, history]);

  const fail = (e: unknown) => setErr(e instanceof Error ? e.message : String(e));

  const loadConversations = useCallback(async () => {
    if (!live) return;
    try {
      setConversations(await chat.conversations());
      setErr(null);
    } catch (e) {
      fail(e);
    }
  }, [live]);

  const loadTurns = useCallback(async (id: string) => {
    try {
      const [ts, as] = await Promise.all([chat.turns(id), chat.artifacts(id).catch(() => [])]);
      setTurns(ts);
      setArtifacts(as);
    } catch (e) {
      fail(e);
    }
  }, []);

  useEffect(() => { void loadConversations(); }, [loadConversations]);

  useEffect(() => {
    if (!live || !activeId) { setTurns([]); setArtifacts([]); return; }
    void loadTurns(activeId);
  }, [live, activeId, loadTurns]);

  /* ── the running turn ─────────────────────────────────────────────── */

  const stopPolling = () => { if (timer.current) window.clearInterval(timer.current); timer.current = null; };

  useEffect(() => {
    if (!pending) return;
    stopPolling();
    const tick = async () => {
      try {
        // Stage progress from the controller, the finished answer from the host.
        // Asking the host for stage detail would mean it proxying the controller;
        // asking the controller for the answer would mean parsing logs for prose.
        const [st, lg, turn] = await Promise.all([
          sandbox.runStatus(pending.runId).catch(() => null),
          sandbox.runLogs(pending.runId).catch(() => ({ logs: {} as Record<string, string> })),
          chat.turn(pending.turnId),
        ]);
        if (st) setRunStatus(st);
        setStageLogs(lg.logs ?? {});
        if (turn.status && turn.status !== "running") {
          stopPolling();
          setPending(null);
          setTurns((prev) => prev.map((x) => (x.id === turn.id ? turn : x)));
          // Hand the finished run's detail to the turn, so the log the person was
          // watching does not vanish the moment the answer arrives.
          setTurnRuns((prev) => ({ ...prev, [turn.id]: { status: st ?? null, logs: lg.logs ?? {} } }));
          if (activeId) void loadTurns(activeId);
          void loadConversations();
          addNotif({
            kind: "agent",
            tone: turn.status === "done" ? "ok" : "error",
            title: t("chat.notif.replied", { agent: pending.agent }),
            detail: active?.title || t("chat.untitled"),
          });
        }
      } catch (e) {
        fail(e);
      }
    };
    void tick();
    timer.current = window.setInterval(() => void tick(), POLL_MS);
    return stopPolling;
    // `active` is read only for a notification label; re-subscribing when the
    // title changes would restart the poll for no reason.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pending, activeId, loadTurns, loadConversations]);

  /* ── conversations ────────────────────────────────────────────────── */

  /** A new conversation binds the first available agent, so it can be talked to
   *  immediately rather than opening on a picker. */
  const defaultRef = useCallback(() => {
    const ref = normalizeRef(active?.templateRef ?? "", tplStores);
    return ref.startsWith("solo:") ? ref : solos[0] ? `solo:${solos[0].id}` : "";
  }, [active?.templateRef, solos, tplStores]);

  const create = useCallback(async () => {
    setBusy("new");
    try {
      const ref = defaultRef();
      const solo = solos.find((s) => `solo:${s.id}` === ref);
      const c = await chat.save({
        title: "",
        templateRef: ref,
        templateLabel: solo ? `Solo — ${solo.name}` : "",
      });
      merge(c);
      setActiveId(c.id);
      setTurns([]);
      setArtifacts([]);
      return c;
    } catch (e) {
      fail(e);
      return null;
    } finally {
      setBusy(null);
    }
  }, [defaultRef, solos, setActiveId]);

  /** Fold a saved conversation back into the list, inserting it if the list has
   *  not caught up with it yet.
   *
   *  `turnCount` and `lastText` are the rail's summaries and are computed only by
   *  the listing query, so a save comes back with them empty. Carrying the old
   *  values over is what stops a row reading "no messages" for a moment after
   *  the conversation is named — which happens right after the first message,
   *  when the row demonstrably has one. */
  const merge = useCallback((next: Conversation) => {
    setConversations((prev) => {
      const old = prev.find((x) => x.id === next.id);
      const merged: Conversation = old
        ? { ...next, turnCount: next.turnCount || old.turnCount, lastText: next.lastText || old.lastText }
        : next;
      return old ? prev.map((x) => (x.id === merged.id ? merged : x)) : [merged, ...prev];
    });
  }, []);

  /**
   * Rename a conversation.
   *
   * Takes the conversation itself rather than an id to look up. Looking it up in
   * `conversations` is what silently broke naming for every NEW chat: a
   * conversation created inside the same send() is not in the array this closure
   * captured, `find` returned undefined, and the rename returned without doing
   * anything — so a fresh chat stayed "untitled" forever while an existing
   * untitled one renamed fine.
   */
  const rename = useCallback(async (conv: Conversation, title: string) => {
    try {
      merge(await chat.save({ ...conv, title }));
    } catch (e) {
      fail(e);
    }
  }, [merge]);

  const bindAgent = useCallback(async (soloId: string) => {
    if (!active) return;
    const solo = solos.find((s) => s.id === soloId);
    try {
      const next = await chat.save({
        ...active,
        templateRef: `solo:${soloId}`,
        templateLabel: solo ? `Solo — ${solo.name}` : "",
      });
      merge(next);
    } catch (e) {
      fail(e);
    }
  }, [active, solos, merge]);

  const setScope = useCallback(async (scope: Conversation["scope"]) => {
    if (!active) return;
    try {
      merge(await chat.save({ ...active, scope: scope ?? undefined }));
    } catch (e) {
      fail(e);
    }
  }, [active, merge]);

  const remove = useCallback(async (id: string) => {
    try {
      await chat.remove(id);
      setConversations((prev) => prev.filter((x) => x.id !== id));
      if (activeId === id) { setActiveId(null); setTurns([]); setArtifacts([]); }
    } catch (e) {
      fail(e);
    }
  }, [activeId, setActiveId]);

  /* ── compaction ───────────────────────────────────────────────────── */

  /**
   * Which agent writes the summaries.
   *
   * The compactor is configuration, not a stage: one tool-less model call the
   * host makes through the gateway. Unset falls back to the agent the
   * conversation is already talking to — summarizing with the model in front of
   * you is never wrong, only sometimes expensive, and the alternative (refusing
   * to compact until something is configured) fails at exactly the moment the
   * conversation is too long to continue.
   */
  const compactor = useMemo(() => {
    const solo = solos.find((s) => s.id === compactorSoloId) ?? agent;
    if (!solo) return null;
    const provider = providers.find((p) => p.name === solo.providerId);
    return { solo, prefix: provider?.prefix ?? "/anthropic/", model: solo.model, system: solo.system };
  }, [solos, compactorSoloId, agent, providers]);

  const compact = useCallback(async (): Promise<boolean> => {
    if (!activeId || history.strategy === "full" || !history.on) return false;
    setBusy("compact");
    setErr(null);
    try {
      const res = await chat.compact({
        conversation: activeId,
        // "recent" is the free path and takes no model at all; only "sum" needs a
        // compactor, and only then is its absence worth mentioning.
        mode: history.strategy === "recent" ? "recent" : "sum",
        keepTurns: history.keepTurns,
        prefix: compactor?.prefix,
        model: compactor?.model,
        system: compactor?.system,
      });
      if (res.turns) setTurns(res.turns);
      if (res.compacted) {
        const saving = { replaced: res.replacedTokens ?? 0, summary: res.summaryTokens ?? 0 };
        setLastSaving(saving);
        // Shared with Settings, so the panel's badge reports what happened
        // instead of a figure written into the markup.
        setLastCompaction(saving);
      }
      setSnoozed(false);
      return !!res.compacted;
    } catch (e) {
      fail(e);
      return false;
    } finally {
      setBusy(null);
    }
  }, [activeId, history, compactor, setLastCompaction]);

  /* ── sending ──────────────────────────────────────────────────────── */

  /**
   * Record a message and start the run that answers it.
   *
   * Compaction happens BEFORE the task is built, because compacting after it
   * would reduce a transcript that has already been sent. When the context is
   * past the auto threshold this is not a suggestion: the alternative is a turn
   * that overflows.
   */
  const send = useCallback(async (text: string, quotes: string[]): Promise<boolean> => {
    const message = text.trim();
    if (!message) return false;
    let conv = active;
    if (!conv) {
      conv = await create();
      if (!conv) return false;
    }
    setBusy("send");
    setErr(null);
    try {
      let current = turns;
      if (ctx.auto && ctx.replaceable > 0) {
        if (await compact()) current = await chat.turns(conv.id);
      }
      setSnoozed(false);

      const ref = normalizeRef(conv.templateRef, tplStores);
      const task = buildTask(buildHistory(current), quotes, message);
      const compiled = compileRef(ref, tplStores, task);
      if (!compiled || compiled.stages.length === 0) {
        throw new Error(t("chat.err.noAgent"));
      }
      const res = await chat.send({
        conversation: conv.id,
        text: message,
        quotes,
        agent: agent?.name ?? compiled.label,
        sinkStage: sinkStageId(compiled.stages),
        runSpec: buildRunSpec(compiled.stages),
      });
      setTurns((prev) => [...prev, res.userTurn, res.agentTurn]);
      setRunStatus(null);
      setStageLogs({});
      setPending({ turnId: res.agentTurn.id, runId: res.runId, agent: res.agentTurn.agent || "" });
      // An untitled conversation takes its name from the first thing asked in it.
      // Awaited: it is one small write, and letting it race meant the rail could
      // redraw from a refresh that landed first and show "untitled" until the
      // next reload.
      if (!conv.title) {
        // Named after the FIRST thing asked in it, which is not always the
        // message being sent: a conversation that never got a name keeps the one
        // it should have had rather than being named after whatever was said in
        // the middle of it. A dropped turn still counts — compaction stops a turn
        // being sent, it does not unsay it.
        const opening = turns.find((x) => x.kind === "user" && x.text.trim())?.text ?? message;
        const title = deriveTitle(opening);
        if (title) await rename(conv, title);
      }
      // And show the message in the rail now. The listing is only refreshed when
      // a reply lands, so without this the row a person just typed into reads
      // "no messages" for as long as the agent takes to answer.
      const id = conv.id;
      setConversations((prev) =>
        prev.map((x) => (x.id === id ? { ...x, lastText: message, turnCount: (x.turnCount ?? 0) + 2 } : x)));
      return true;
    } catch (e) {
      fail(e);
      return false;
    } finally {
      setBusy(null);
    }
  }, [active, agent, create, compact, ctx, rename, t, tplStores, turns]);

  /** Stop the running turn. The reply it has written so far still lands — the
   *  host records what the stopped run left in its manifest. */
  const stop = useCallback(async () => {
    if (!pending) return;
    try {
      await sandbox.runStop(pending.runId);
    } catch (e) {
      fail(e);
    }
  }, [pending]);

  /** Re-ask the last question, replacing nothing: the previous answer stays in
   *  the transcript, because hiding a bad answer the person already read is a
   *  worse trade than a slightly longer history. */
  const regenerate = useCallback(async () => {
    const lastUser = [...turns].reverse().find((x) => x.kind === "user" && !x.dropped);
    if (!lastUser) return;
    await send(lastUser.text, []);
  }, [turns, send]);

  /** Fetch one past turn's run detail. The controller answers from its stage-log
   *  archive after the containers are gone, so this works for a turn from last
   *  week; a run whose archive has aged out simply has no steps to show. */
  const loadTurnRun = useCallback(async (turnId: number, runId: string) => {
    if (!runId || turnRuns[turnId]) return;
    try {
      const [st, lg] = await Promise.all([
        sandbox.runStatus(runId).catch(() => null),
        sandbox.runLogs(runId).catch(() => ({ logs: {} as Record<string, string> })),
      ]);
      setTurnRuns((prev) => ({ ...prev, [turnId]: { status: st, logs: lg.logs ?? {} } }));
    } catch {
      // A log that cannot be read is not worth an error banner over an answer
      // that is already on screen.
    }
  }, [turnRuns]);

  const attach = useCallback(async (file: File) => {
    if (!activeId) return;
    setBusy("attach");
    try {
      await chat.attach(activeId, file);
      setArtifacts(await chat.artifacts(activeId));
    } catch (e) {
      fail(e);
    } finally {
      setBusy(null);
    }
  }, [activeId]);

  return {
    live,
    conversations,
    active,
    activeId,
    select: setActiveId,
    turns,
    artifacts,
    agent,
    ctx,
    pending,
    runStatus,
    stageLogs,
    turnRuns,
    loadTurnRun,
    busy,
    err,
    clearErr: () => setErr(null),
    snoozed,
    snooze: () => setSnoozed(true),
    lastSaving,
    compactor,
    create,
    rename,
    remove,
    bindAgent,
    setScope,
    send,
    stop,
    regenerate,
    compact,
    attach,
    refresh: loadConversations,
  };
}
