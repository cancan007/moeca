import { useMemo, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import type { Turn } from "@/lib/chat";
import type { Artifact } from "@/lib/daily";
import type { RunStatus } from "@/lib/sandbox";
import type { SoloAgent } from "@/lib/templates";
import { Avatar, ExtBadge, ReplyIcon, statusColor } from "./ui";
import { fmtTokens, runSteps, stepTotals, type Step } from "./steps";

/**
 * The transcript.
 *
 * Agent text is rendered as PLAIN paragraphs, not as markdown. The renderer this
 * app ships (lib/markdown.ts) says in its own header that its correctness is not
 * load-bearing for safety because its output only ever goes into a sandboxed
 * iframe — so putting agent-written HTML into the app's own origin, the origin
 * holding the loopback services and the admin token, would be borrowing a
 * guarantee that was never made. The design draws paragraphs, and paragraphs are
 * also the safe answer.
 *
 * A dropped turn is not hidden. It sits under the summary that replaced it,
 * collapsed, because "what did compaction throw away" is a question the operator
 * is entitled to answer without going to the database.
 */

/** Blank-line-separated paragraphs, which is as much structure as plain text has. */
function paragraphs(text: string): string[] {
  return (text ?? "").split(/\n{2,}/).map((p) => p.trim()).filter(Boolean);
}

/** A turn's artifacts: the files whose mtime falls after the turn started. The
 *  conversation shares one directory, so this is the only way to say which reply
 *  produced what — and it is honest about being approximate by only ever listing
 *  files against the turn that was running when they appeared. */
function turnFiles(turn: Turn, artifacts: Artifact[], nextAt: number | undefined): Artifact[] {
  const from = Date.parse(turn.createdAt.replace(" ", "T") + (/[zZ]|[+-]\d\d:?\d\d$/.test(turn.createdAt) ? "" : "Z"));
  if (Number.isNaN(from)) return [];
  return artifacts.filter((a) => {
    const at = Date.parse(a.modTime);
    return !Number.isNaN(at) && at >= from && (nextAt === undefined || at < nextAt);
  });
}

const actionStyle: CSSProperties = {
  font: "500 10px 'IBM Plex Sans'", color: "var(--tx-faint)",
  padding: "4px 8px", borderRadius: 6, cursor: "pointer",
};

function StepRow({ step }: { step: Step }) {
  const { t } = useTranslation();
  let tag = "";
  let text = "";
  let dur = "";
  let tone = "var(--tx3)";
  switch (step.kind) {
    case "stage":
      tag = "stage";
      text = `${step.name}${step.role ? ` · ${step.role}` : ""}`;
      dur = t(`chat.log.status.${step.status}`, { defaultValue: step.status });
      tone = statusColor(step.status);
      break;
    case "turn":
      tag = "turn";
      text = step.tools.length
        ? t("chat.log.calling", { tools: step.tools.join(", ") })
        : t("chat.log.answered");
      dur = step.tokens ? `${fmtTokens(step.tokens)} tok` : "";
      break;
    case "tool":
      tag = "tool";
      text = step.name;
      dur = step.error ? t("chat.log.failed") : "";
      tone = step.error ? "var(--red)" : "var(--tx3)";
      break;
    case "compaction":
      tag = "compact";
      text = t("chat.log.compacted", { before: step.before, after: step.after });
      dur = step.tokens ? `${fmtTokens(step.tokens)} tok` : "";
      break;
    case "search":
      tag = "search";
      // The action matters as much as the count: one OpenAI grant covers
      // searching, opening a page and reading it, and each is billed.
      text = step.query
        ? t(`chat.log.search.${step.action}`, { defaultValue: step.action, q: step.query })
        : t(`chat.log.search.${step.action}`, { defaultValue: step.action, q: "" });
      // Pictures are called out: they came from the provider's side, so no
      // gateway saw them, and each one is paid for in tokens.
      if (step.images > 0) text += ` · ${t("chat.log.searchImages", { count: step.images })}`;
      dur = step.limit > 0 ? `${step.used}/${step.limit}` : String(step.used);
      tone = "#5b9fe8";
      break;
    case "searchLimit":
      tag = "search";
      text = t("chat.log.searchLimit", { used: step.used, limit: step.limit });
      dur = `${step.used}/${step.limit}`;
      tone = "#e0a83e";
      break;
    case "handoff":
      tag = "files";
      text = step.files.join(", ");
      dur = String(step.files.length);
      break;
    case "error":
      tag = "error";
      text = step.message;
      tone = "var(--red)";
      break;
  }
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
      <span
        style={{
          font: "600 8.5px 'IBM Plex Mono'", color: tone, background: "var(--bg-card)",
          padding: "2px 6px", borderRadius: 4, flex: "none", letterSpacing: ".04em",
        }}
      >
        {tag}
      </span>
      <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: tone === "var(--red)" ? tone : "var(--tx3)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {text}
      </span>
      <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: "none" }}>{dur}</span>
    </div>
  );
}

/** The collapsible account of how a reply was produced.
 *
 *  `loadable` is a turn whose log has not been fetched yet — a past turn, whose
 *  steps the controller still holds in its archive. It renders the header so the
 *  log can be asked for, rather than hiding the fact that there is one. */
function WorkLog({
  steps,
  open,
  loadable,
  loaded,
  onToggle,
}: {
  steps: Step[];
  open: boolean;
  loadable?: boolean;
  /** The run detail was fetched. With no steps in it the row stays and says so:
   *  a row that vanishes on the click that opened it reads as a broken button,
   *  and "this run left no readable log" is a real answer. */
  loaded?: boolean;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  const totals = useMemo(() => stepTotals(steps), [steps]);
  if (steps.length === 0 && !loadable && !loaded) return null;
  return (
    <div style={{ display: "flex", flexDirection: "column" }}>
      <div onClick={onToggle} style={{ display: "flex", alignItems: "center", gap: 8, padding: "2px 0", cursor: "pointer", alignSelf: "flex-start", maxWidth: "100%" }}>
        <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="var(--tx-dim)" strokeWidth="1.5">
          <path d="M3 4h10M3 8h7M3 12h4" />
        </svg>
        <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx3)", whiteSpace: "nowrap", flex: "none" }}>
          {t("chat.log.title")}
        </span>
        <span style={{ font: "400 10px 'IBM Plex Mono'", color: totals.errors ? "var(--red)" : "var(--tx-faint)", whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", minWidth: 0 }}>
          {steps.length > 0
            ? t("chat.log.meta", { tools: totals.tools, tokens: fmtTokens(totals.tokens) })
            : loaded
              ? t("chat.log.none")
              : t("common.loading")}
          {/* Searches are called out separately because nothing else in this
              line reflects them: they are billed per use, not per token. */}
          {totals.searches > 0 ? ` · ${t("chat.log.searches", { count: totals.searches })}` : ""}
          {totals.errors > 0 ? ` · ${t("chat.log.errors", { count: totals.errors })}` : ""}
        </span>
        <span style={{ font: "400 12px 'IBM Plex Sans'", color: "var(--tx-faint)", transform: open ? "rotate(90deg)" : "none", transition: "transform .16s" }}>›</span>
      </div>
      {open && (
        <div style={{ animation: "ocRise .18s cubic-bezier(.2,.8,.2,1) both", margin: "8px 0 0 5px", padding: "2px 0 2px 14px", borderLeft: "1px solid var(--bd)", display: "flex", flexDirection: "column", gap: 6 }}>
          {steps.map((s, i) => <StepRow key={i} step={s} />)}
        </div>
      )}
    </div>
  );
}

export function ChatThread({
  turns,
  artifacts,
  agent,
  runStatus,
  stageLogs,
  turnRuns,
  onLoadTurnRun,
  pendingTurnId,
  onQuote,
  onOpenFile,
  onRegenerate,
  onPromote,
  scrollRef,
  onSelectionUp,
}: {
  turns: Turn[];
  artifacts: Artifact[];
  agent: SoloAgent | undefined;
  runStatus: RunStatus | null;
  stageLogs: Record<string, string>;
  /** Run detail already fetched for a past turn, keyed by turn id. */
  turnRuns: Record<number, { status: RunStatus | null; logs: Record<string, string> }>;
  onLoadTurnRun: (turnId: number, runId: string) => void;
  pendingTurnId: number | null;
  onQuote: (text: string, from: string) => void;
  onOpenFile: (a: Artifact) => void;
  onRegenerate: () => void;
  onPromote: (text: string) => void;
  scrollRef: React.RefObject<HTMLDivElement>;
  onSelectionUp: (e: React.MouseEvent) => void;
}) {
  const { t } = useTranslation();
  const [openLogs, setOpenLogs] = useState<Record<number, boolean>>({});
  const [openSummaries, setOpenSummaries] = useState<Record<number, boolean>>({});
  const [copied, setCopied] = useState<number | null>(null);

  const liveSteps = useMemo(() => runSteps(runStatus, stageLogs), [runStatus, stageLogs]);

  // A dropped turn is drawn under the summary that replaced it, not in sequence.
  //
  // Grouped by display order rather than by the summary's covered seq range: a
  // briefing folded into a later one is itself dropped, and its own seq is above
  // the range that replaced it, so a range test would orphan exactly the rows
  // that most need to be shown together. Turns arrive in reading order, so the
  // dropped ones accumulate until the summary that stands for them appears.
  const dropped = useMemo(() => {
    const bySummary = new Map<number, Turn[]>();
    let pending: Turn[] = [];
    for (const item of turns) {
      if (item.dropped) { pending.push(item); continue; }
      if (item.kind === "summary") { bySummary.set(item.id, pending); pending = []; }
    }
    return bySummary;
  }, [turns]);

  const visible = turns.filter((x) => !x.dropped);

  const copy = async (turn: Turn) => {
    try {
      await navigator.clipboard.writeText(turn.text);
      setCopied(turn.id);
      window.setTimeout(() => setCopied((c) => (c === turn.id ? null : c)), 1200);
    } catch {
      /* a clipboard the browser refuses is not worth an error banner */
    }
  };

  return (
    <div ref={scrollRef} onMouseUp={onSelectionUp} style={{ flex: 1, overflowY: "auto", display: "flex", justifyContent: "center" }}>
      <div style={{ width: "100%", maxWidth: 740, padding: "24px 32px 48px", display: "flex", flexDirection: "column", gap: 38 }}>
        {visible.length === 0 && (
          <div style={{ padding: "90px 0 0", display: "flex", flexDirection: "column", alignItems: "center", gap: 10 }}>
            <span style={{ font: "600 17px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("chat.empty.title")}</span>
            <span style={{ font: "400 12px 'IBM Plex Sans'", color: "var(--tx-dim)", textAlign: "center", lineHeight: 1.7, maxWidth: 420 }}>
              {t("chat.empty.body")}
            </span>
          </div>
        )}

        {visible.map((turn, i) => {
          if (turn.kind === "user") {
            return (
              <div key={turn.id} style={{ animation: "ocRise .22s cubic-bezier(.2,.8,.2,1) both", display: "flex", flexDirection: "column", alignItems: "flex-end", gap: 6 }}>
                {(turn.quotes ?? []).map((q, qi) => (
                  <div key={qi} style={{ maxWidth: "78%", display: "flex", alignItems: "flex-start", gap: 8, padding: "2px 4px" }}>
                    <div style={{ flex: "none", marginTop: 3 }}><ReplyIcon color="var(--tx-faint)" /></div>
                    <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6, display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical", overflow: "hidden" }}>
                      {q}
                    </span>
                  </div>
                ))}
                <div
                  data-ch-role="user"
                  style={{ maxWidth: "78%", background: "var(--bg-card)", borderRadius: 18, padding: "11px 16px", font: "400 13px 'IBM Plex Sans'", color: "var(--tx)", lineHeight: 1.7, whiteSpace: "pre-wrap" }}
                >
                  {turn.text}
                </div>
              </div>
            );
          }

          if (turn.kind === "summary") {
            const open = !!openSummaries[turn.id];
            const covered = dropped.get(turn.id) ?? [];
            return (
              <div key={turn.id} style={{ display: "flex", flexDirection: "column", gap: 10 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
                  <div style={{ flex: 1, height: 1, background: "var(--bd)" }} />
                  <div
                    onClick={() => setOpenSummaries((s) => ({ ...s, [turn.id]: !open }))}
                    style={{ display: "flex", alignItems: "center", gap: 7, cursor: "pointer", font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", whiteSpace: "nowrap" }}
                  >
                    <svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
                      <path d="M4 3v10M8 5v6M12 7v2" />
                    </svg>
                    {t("chat.summary.label", { count: covered.length })}
                    <span style={{ transform: open ? "rotate(90deg)" : "none", transition: "transform .16s" }}>›</span>
                  </div>
                  <div style={{ flex: 1, height: 1, background: "var(--bd)" }} />
                </div>
                {open && (
                  <div style={{ animation: "ocRise .18s cubic-bezier(.2,.8,.2,1) both", background: "var(--bg-panel)", borderRadius: 12, padding: "12px 16px", display: "flex", flexDirection: "column", gap: 8 }}>
                    <span style={{ font: "600 9.5px 'IBM Plex Mono'", letterSpacing: ".1em", color: "var(--tx-faint)" }}>
                      {t("chat.summary.heading")}
                    </span>
                    {paragraphs(turn.text).map((p, pi) => (
                      <span key={pi} style={{ font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx3)", lineHeight: 1.7, whiteSpace: "pre-wrap" }}>{p}</span>
                    ))}
                    {covered.length > 0 && (
                      <div style={{ borderTop: "1px solid var(--bd-soft)", marginTop: 4, paddingTop: 8, display: "flex", flexDirection: "column", gap: 6 }}>
                        <span style={{ font: "600 9.5px 'IBM Plex Mono'", letterSpacing: ".1em", color: "var(--tx-faint)" }}>
                          {t("chat.summary.replaced")}
                        </span>
                        {covered.map((c) => (
                          <span key={c.id} style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                            {c.kind === "user" ? "› " : "‹ "}{c.text}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            );
          }

          // An agent turn. While it is the running one its log is the live one;
          // afterwards it is whatever has been fetched for that turn.
          const isPending = turn.id === pendingTurnId;
          const fetched = turnRuns[turn.id];
          const steps = isPending
            ? liveSteps
            : fetched
              ? runSteps(fetched.status, fetched.logs)
              : [];
          const loadable = !isPending && !fetched && !!turn.runId;
          const toggleLog = () => {
            if (loadable) onLoadTurnRun(turn.id, turn.runId ?? "");
            setOpenLogs((s) => ({ ...s, [turn.id]: !s[turn.id] }));
          };
          const nextAt = (() => {
            const next = visible[i + 1];
            if (!next) return undefined;
            const p = Date.parse(next.createdAt.replace(" ", "T") + (/[zZ]|[+-]\d\d:?\d\d$/.test(next.createdAt) ? "" : "Z"));
            return Number.isNaN(p) ? undefined : p;
          })();
          const files = turnFiles(turn, artifacts, nextAt);

          if (isPending && !turn.text) {
            return (
              <div key={turn.id} style={{ animation: "ocFade .2s ease-out both", display: "flex", gap: 12 }}>
                <Avatar agent={agent} name={turn.agent} />
                <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 12 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8, height: 24 }}>
                    <div style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--ac)", boxShadow: "0 0 8px var(--ac)" }} />
                    <span style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx3)" }}>
                      {t("chat.working", { agent: turn.agent || agent?.name || "" })}
                    </span>
                  </div>
                  <WorkLog steps={steps} open={openLogs[turn.id] ?? true} onToggle={() => setOpenLogs((s) => ({ ...s, [turn.id]: !(s[turn.id] ?? true) }))} />
                </div>
              </div>
            );
          }

          return (
            <div key={turn.id} style={{ animation: "ocRise .24s cubic-bezier(.2,.8,.2,1) both", display: "flex", gap: 12 }}>
              <Avatar agent={agent} name={turn.agent} />
              <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 14 }}>
                <div style={{ display: "flex", alignItems: "center", gap: 8, height: 24 }}>
                  <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{turn.agent || agent?.name}</span>
                  {turn.status && turn.status !== "done" && (
                    <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: statusColor(turn.status) }}>
                      {t(`chat.status.${turn.status}`, { defaultValue: turn.status })}
                    </span>
                  )}
                </div>

                <WorkLog steps={steps} open={!!openLogs[turn.id]} loadable={loadable} loaded={!!fetched} onToggle={toggleLog} />

                {paragraphs(turn.text).map((p, pi) => (
                  <span key={pi} style={{ font: "400 13px 'IBM Plex Sans'", color: "var(--tx2)", lineHeight: 1.8, whiteSpace: "pre-wrap" }}>{p}</span>
                ))}

                {!turn.text && (
                  <span style={{ font: "400 12px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.7 }}>
                    {t(turn.status === "empty" ? "chat.noReply" : "chat.noReplyFailed")}
                  </span>
                )}

                {files.length > 0 && (
                  <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(210px, 1fr))", gap: 8 }}>
                    {files.map((f) => (
                      <div
                        key={f.path}
                        onClick={() => onOpenFile(f)}
                        style={{ display: "flex", alignItems: "center", gap: 11, background: "var(--bg-card)", borderRadius: 10, padding: "9px 11px", cursor: "pointer", minWidth: 0 }}
                      >
                        <ExtBadge name={f.name} kind={f.kind} />
                        <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 2 }}>
                          <span style={{ font: "500 11.5px 'IBM Plex Mono'", color: "var(--tx)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{f.name}</span>
                          <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{t("chat.openToPreview")}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                )}

                <div style={{ display: "flex", alignItems: "center", gap: 2, marginLeft: -8 }}>
                  <div onClick={() => void copy(turn)} style={actionStyle}>
                    {copied === turn.id ? t("chat.actions.copied") : t("chat.actions.copy")}
                  </div>
                  <div onClick={onRegenerate} style={actionStyle}>{t("chat.actions.regenerate")}</div>
                  <div onClick={() => onQuote(turn.text, turn.agent || agent?.name || "")} style={actionStyle}>{t("chat.actions.reply")}</div>
                  <div onClick={() => onPromote(turn.text)} style={actionStyle}>{t("chat.actions.promote")}</div>
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
