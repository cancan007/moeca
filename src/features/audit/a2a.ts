// a2a.ts — reconstruct an A2A-style semantic tree from the gateway's raw
import i18n from "@/i18n";
// request/response capture.
//
// The gateway is the single chokepoint every sandboxed agent call must pass, so
// its access log holds the *actual* prompt (reqBody) and model output (respBody)
// for each LLM turn — see gateway/internal/gateway/logging.go. Here we parse that
// raw capture (Anthropic Messages JSON; the agent is non-streaming, so respBody
// is a single JSON object with a `content` block list) into the same Node/Part
// model the mock A2A trace uses, so the live view renders the agent's
// interpretation and output — not just an HTTP request row.
//
// Hierarchy:  Context (run) → Task (stage) → Message (one LLM call)
//                → Part per content block: input · thinking · tool_use · text
//                                  → Artifact (what the stage produced)
//
// Artifacts do not come from the gateway. The gateway sees network traffic, and
// a stage's output is a filesystem effect — it never crosses the wire, so the
// access log cannot describe it. The orchestrator records each stage boundary as
// a commit in the worktree and reports the files it touched, so run status is a
// second source joined in here by run id. Without it the tree can show what an
// agent *asked* to write (the tool_use part) but not what actually landed.
//
// This module is pure (no React, no styles) so it is unit-testable.

import type { AccessLog } from "@/lib/gateway";
import type { RunStatus } from "@/lib/sandbox";

export type Kind = "Context" | "Task" | "Artifact" | "Message" | "Part" | "Extensions" | "Metadata";

// Layer distinguishes the semantic role of a Part so the view can colour the
// agent's reasoning (thinking) apart from its actions (tool_use) and output
// (text). "input" is the prompt turn; "raw" is an unparseable capture.
export type Layer = "input" | "thinking" | "tool_use" | "text" | "raw" | "system";

export interface Part {
  ptype: "text" | "file" | "data";
  label: string;
  crumb: string;
  fields: { k: string; v: string }[];
  body: { kind: "text" | "code" | "json"; content: string };
  layer?: Layer;
}

export interface Node {
  id: string;
  depth: number;
  ancestors: string[];
  kind: Kind;
  label: string;
  sub: string;
  tok: string;
  hasChildren: boolean;
  ts: string;
  fields: { k: string; v: string }[];
  parts?: Part[];
  ext?: { k: string; v: string }[];
}

/* ── token / time formatting ──────────────────────────────────────── */

export function fmtTokens(n: number): string {
  if (!n) return "0";
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

function fmtClock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleTimeString("ja-JP", { hour12: false });
}

function preview(s: string, n = 64): string {
  const flat = s.replace(/\s+/g, " ").trim();
  return flat.length > n ? flat.slice(0, n - 1) + "…" : flat;
}

/* ── block parsing (Anthropic Messages dialect) ───────────────────── */

interface RawPart {
  layer: Layer;
  ptype: Part["ptype"];
  label: string;
  body: Part["body"];
  fields: { k: string; v: string }[];
}

// parseResponseBlocks turns a captured response body into ordered layer parts.
// The response is `{ content: [ {type:"thinking"|"text"|"tool_use", ...} ] }`.
// If the capture was truncated (JSON won't parse) we surface it as one raw part
// so nothing is silently dropped.
export function parseResponseBlocks(respBody?: string): RawPart[] {
  if (!respBody) return [];
  let obj: unknown;
  try {
    obj = JSON.parse(respBody);
  } catch {
    return [{ layer: "raw", ptype: "text", label: preview(respBody), body: { kind: "text", content: respBody }, fields: [{ k: "note", v: "truncated / non-JSON capture" }] }];
  }
  const content = (obj as { content?: unknown[] }).content;
  if (!Array.isArray(content)) {
    return [{ layer: "raw", ptype: "text", label: "response", body: { kind: "json", content: safeStringify(obj) }, fields: [] }];
  }
  const out: RawPart[] = [];
  for (const b of content) {
    const block = b as Record<string, unknown>;
    switch (block.type) {
      case "thinking":
      case "redacted_thinking": {
        const text = String(block.thinking ?? block.text ?? "[redacted]");
        out.push({ layer: "thinking", ptype: "text", label: preview(text), body: { kind: "text", content: text }, fields: [{ k: "type", v: "thinking" }, { k: "chars", v: String(text.length) }] });
        break;
      }
      case "text": {
        const text = String(block.text ?? "");
        out.push({ layer: "text", ptype: "text", label: preview(text), body: { kind: "text", content: text }, fields: [{ k: "type", v: "text" }, { k: "chars", v: String(text.length) }] });
        break;
      }
      case "tool_use": {
        const name = String(block.name ?? "tool");
        out.push({ layer: "tool_use", ptype: "data", label: name, body: { kind: "json", content: safeStringify(block.input ?? {}) }, fields: [{ k: "type", v: "tool_use" }, { k: "name", v: name }, { k: "id", v: String(block.id ?? "—") }] });
        break;
      }
      default:
        out.push({ layer: "raw", ptype: "data", label: String(block.type ?? "block"), body: { kind: "json", content: safeStringify(block) }, fields: [{ k: "type", v: String(block.type ?? "?") }] });
    }
  }
  return out;
}

// parseRequestInput turns the captured request into the prompt turn: the system
// prompt (if any) plus the newest message's content blocks (what the agent was
// actually asked on this turn).
export function parseRequestInput(reqBody?: string): RawPart[] {
  if (!reqBody) return [];
  let obj: Record<string, unknown>;
  try {
    obj = JSON.parse(reqBody) as Record<string, unknown>;
  } catch {
    return [{ layer: "raw", ptype: "text", label: preview(reqBody), body: { kind: "text", content: reqBody }, fields: [{ k: "note", v: "truncated / non-JSON capture" }] }];
  }
  const out: RawPart[] = [];
  if (typeof obj.system === "string" && obj.system.trim()) {
    out.push({ layer: "system", ptype: "text", label: preview(obj.system), body: { kind: "text", content: obj.system }, fields: [{ k: "type", v: "system" }, { k: "chars", v: String(obj.system.length) }] });
  }
  const messages = obj.messages;
  if (Array.isArray(messages) && messages.length) {
    const last = messages[messages.length - 1] as { role?: string; content?: unknown };
    const role = String(last.role ?? "user");
    const content = last.content;
    if (typeof content === "string") {
      out.push({ layer: "input", ptype: "text", label: preview(content), body: { kind: "text", content }, fields: [{ k: "role", v: role }] });
    } else if (Array.isArray(content)) {
      for (const b of content) {
        const block = b as Record<string, unknown>;
        if (block.type === "text") {
          const t = String(block.text ?? "");
          out.push({ layer: "input", ptype: "text", label: preview(t), body: { kind: "text", content: t }, fields: [{ k: "role", v: role }, { k: "type", v: "text" }] });
        } else if (block.type === "tool_result") {
          const c = typeof block.content === "string" ? block.content : safeStringify(block.content ?? "");
          out.push({ layer: "input", ptype: "data", label: `tool_result${block.is_error ? " (error)" : ""}`, body: { kind: "json", content: c }, fields: [{ k: "role", v: role }, { k: "type", v: "tool_result" }, { k: "toolUseId", v: String(block.tool_use_id ?? "—") }] });
        } else if (block.type === "tool_use") {
          out.push({ layer: "input", ptype: "data", label: `tool_use: ${String(block.name ?? "")}`, body: { kind: "json", content: safeStringify(block.input ?? {}) }, fields: [{ k: "role", v: role }, { k: "type", v: "tool_use" }] });
        }
      }
    }
  }
  return out;
}

function safeStringify(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}

/* ── tree assembly ────────────────────────────────────────────────── */

// A short, stable label for logs that carry no run id (solo/ad-hoc agents get
// grouped by session instead).
function ctxKeyOf(l: AccessLog): string {
  return l.run || `session:${(l.session || "unknown").slice(0, 8)}`;
}

// buildLiveTree flattens grouped calls into the depth/ancestors Node[] shape the
// tree renderer consumes. Only model calls (those with captured content) become
// rich Message nodes; other gateway traffic (github, fetch, rag) is still shown
// as a Message leaf so the audit trail is complete.
export function buildLiveTree(logs: AccessLog[], runs?: Map<string, RunStatus>): Node[] {
  const calls = [...logs].sort((a, b) => a.time.localeCompare(b.time));
  const nodes: Node[] = [];

  // group: context (run) → task (stage) → calls
  const ctxOrder: string[] = [];
  const byCtx = new Map<string, AccessLog[]>();
  for (const l of calls) {
    const k = ctxKeyOf(l);
    if (!byCtx.has(k)) { byCtx.set(k, []); ctxOrder.push(k); }
    byCtx.get(k)!.push(l);
  }

  for (const ck of ctxOrder) {
    const ctxCalls = byCtx.get(ck)!;
    const ctxId = `ctx:${ck}`;
    const ctxTokens = ctxCalls.reduce((s, l) => s + (l.tokensEst ?? 0), 0);
    const models = uniq(ctxCalls.map((l) => l.model).filter(Boolean) as string[]);
    const stages = uniq(ctxCalls.map((l) => l.stage || "").filter(Boolean));
    nodes.push({
      id: ctxId, depth: 0, ancestors: [], kind: "Context",
      label: ck.startsWith("session:") ? "solo / ad-hoc" : ck,
      sub: ck, tok: fmtTokens(ctxTokens), hasChildren: true, ts: fmtClock(ctxCalls[0].time),
      fields: [
        { k: "contextId", v: ck },
        { k: "calls", v: String(ctxCalls.length) },
        { k: "stages", v: stages.length ? String(stages.length) : "—" },
        { k: "models", v: models.join(", ") || "—" },
        { k: "tokensEst", v: fmtTokens(ctxTokens) },
      ],
    });

    // stage grouping
    const stageOrder: string[] = [];
    const byStage = new Map<string, AccessLog[]>();
    for (const l of ctxCalls) {
      const sk = l.stage || "main";
      if (!byStage.has(sk)) { byStage.set(sk, []); stageOrder.push(sk); }
      byStage.get(sk)!.push(l);
    }

    for (const sk of stageOrder) {
      const stageCalls = byStage.get(sk)!;
      const taskId = `task:${ck}:${sk}`;
      const stageTokens = stageCalls.reduce((s, l) => s + (l.tokensEst ?? 0), 0);
      nodes.push({
        id: taskId, depth: 1, ancestors: [ctxId], kind: "Task",
        label: sk, sub: `${stageCalls.length} calls`, tok: fmtTokens(stageTokens),
        hasChildren: true, ts: fmtClock(stageCalls[0].time),
        fields: [
          { k: "stageId", v: sk },
          { k: "calls", v: String(stageCalls.length) },
          { k: "tokensEst", v: fmtTokens(stageTokens) },
        ],
      });

      stageCalls.forEach((l, i) => {
        const callId = `call:${l.requestId}`;
        const respParts = parseResponseBlocks(l.respBody);
        const reqParts = parseRequestInput(l.reqBody);
        const allParts = [...reqParts, ...respParts];
        const svcLabel = l.service === "anthropic" || l.model
          ? `LLM call #${i + 1}`
          : `${l.service} ${l.method}`;
        nodes.push({
          id: callId, depth: 2, ancestors: [ctxId, taskId], kind: "Message",
          label: svcLabel, sub: l.model || l.path, tok: fmtTokens(l.tokensEst ?? 0),
          hasChildren: allParts.length > 0, ts: fmtClock(l.time),
          fields: [
            { k: "requestId", v: l.requestId },
            { k: "service", v: l.service },
            { k: "model", v: l.model || "—" },
            { k: "status", v: String(l.status) },
            { k: "inputTokens", v: String(l.inputTokens ?? 0) },
            { k: "outputTokens", v: String(l.outputTokens ?? 0) },
            { k: "durationMs", v: String(l.durationMs) },
            { k: "path", v: l.path },
          ],
        });

        allParts.forEach((p, pi) => {
          const partNode: Part = {
            ptype: p.ptype, label: p.label,
            crumb: `${sk} › ${l.requestId.slice(0, 8)} › ${p.layer}[${pi}]`,
            fields: p.fields, body: p.body, layer: p.layer,
          };
          nodes.push({
            id: `${callId}:p${pi}`, depth: 3, ancestors: [ctxId, taskId, callId], kind: "Part",
            label: layerLabel(p.layer, p.label), sub: p.layer,
            tok: "", hasChildren: false, ts: fmtClock(l.time),
            fields: p.fields, parts: [partNode],
          });
        });
      });

      // What the stage produced, from the run status rather than the log. Sits
      // after the calls that led to it, as the outcome of the task.
      const artifact = artifactNode(runs?.get(ck), sk, ctxId, taskId);
      if (artifact) nodes.push(artifact);
    }
  }

  return nodes;
}

/** Build the Artifact node for one stage, or null when it produced nothing. */
function artifactNode(run: RunStatus | undefined, stageID: string, ctxId: string, taskId: string): Node | null {
  const stage = run?.stages?.find((s) => s.id === stageID);
  const files = stage?.files ?? [];
  if (!stage?.commit || files.length === 0) return null;

  // Binary files report -1 rather than a line count; don't sum those into a
  // total that would read as real lines.
  const add = files.reduce((n, f) => n + Math.max(0, f.additions), 0);
  const del = files.reduce((n, f) => n + Math.max(0, f.deletions), 0);
  const short = stage.commit.slice(0, 8);

  return {
    id: `artifact:${run!.id}:${stageID}`,
    depth: 2,
    ancestors: [ctxId, taskId],
    kind: "Artifact",
    label: files.length === 1 ? files[0].path : i18n.t("audit.fileCount", { count: files.length }),
    sub: `${short} · +${add} −${del}`,
    tok: "",
    hasChildren: true,
    ts: "",
    fields: [
      { k: "commit", v: short },
      { k: "parent", v: stage.parent ? stage.parent.slice(0, 8) : "—" },
      { k: "files", v: String(files.length) },
      { k: "additions", v: String(add) },
      { k: "deletions", v: String(del) },
    ],
    // One Part per file: an Artifact's parts are its contents, same as a Message.
    parts: files.map((f) => ({
      layer: "text" as Layer,
      ptype: "file" as const,
      label: f.path,
      crumb: `${stageID} › ${short} › ${f.path}`,
      body: {
        kind: "text" as const,
        content:
          f.additions < 0 || f.deletions < 0
            ? `${f.path}\n(binary)`
            : `${f.path}\n+${f.additions} −${f.deletions}`,
      },
      fields: [
        { k: "path", v: f.path },
        { k: "additions", v: f.additions < 0 ? "binary" : String(f.additions) },
        { k: "deletions", v: f.deletions < 0 ? "binary" : String(f.deletions) },
      ],
    })),
  };
}

function layerLabel(layer: Layer, label: string): string {
  const tag: Record<Layer, string> = {
    system: "system", input: i18n.t("audit.partInput"), thinking: "thinking", tool_use: "tool", text: i18n.t("audit.partOutput"), raw: "raw",
  };
  return `${tag[layer]} · ${label}`;
}

function uniq<T>(xs: T[]): T[] {
  return Array.from(new Set(xs));
}
