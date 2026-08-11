// Custom tool definitions (HTTP through the gateway). A tool is authored in the
import i18n from "@/i18n";
// UI (Settings → Tools), assigned per-Solo, and compiled to the agent's HTTP
// tool shape at run time. The tool never holds keys — its `path` targets a
// gateway route (a Proxy provider) that injects credentials and enforces policy.

export interface ToolParam {
  name: string;
  type: string; // "string" | "number" | "boolean"
  description: string;
  required: boolean;
}

/** What a tool's response becomes.
 *
 *  A tool that answers with information returns its body to the model. A tool
 *  that MAKES something cannot — a generated image is hundreds of kilobytes of
 *  base64, and the model could only hand it to write_file, which would write
 *  the string rather than the bytes it encodes. An artifact output decodes the
 *  response and writes it into /work instead, and the model is told only where
 *  the file went.
 *
 *  This is what makes image / speech / video generation ordinary tools rather
 *  than a second mechanism hardcoded to one vendor's request and response
 *  shapes. */
export interface ToolOutput {
  /** "text" (default) returns the body to the model; "binary" writes the body
   *  itself; "base64" writes the decoded value found at jsonPath. */
  kind: "text" | "binary" | "base64";
  /** Where the payload sits in a JSON response, dot-separated, numeric segments
   *  indexing arrays — "data.0.b64_json", "predictions.0.bytesBase64Encoded". */
  jsonPath?: string;
  /** Extensions the model's chosen output path must end in. A generated file
   *  that lands as `.sh` is not an artifact, so the tool refuses. */
  extensions?: string[];
  /** Set for an asynchronous job: create, poll, then download. */
  poll?: ToolPoll;
}

/** The create → poll → download shape long generations use. The tool call is
 *  held open across the wait rather than handing the model a job id it would
 *  have to remember to check. */
export interface ToolPoll {
  idPath?: string;      // default "id"
  statusPath?: string;  // default "status"
  errorPath?: string;   // default "error"
  done?: string[];      // statuses meaning finished
  fail?: string[];      // statuses meaning gave up
  /** Appended to the tool path with {{id}} substituted. Defaults "/{{id}}" and
   *  "/{{id}}/content". */
  statusUrl?: string;
  resultUrl?: string;
  everySec?: number;
  forSec?: number;
}

export interface ToolDef {
  id: string;
  name: string;
  description: string;
  params: ToolParam[];
  method: string;
  /** Gateway-relative path, e.g. "/slack/chat.postMessage" or "/fetch/". */
  path: string;
  headers: Record<string, string>;
  body: string;
  /** For /fetch dynamic targets (X-Orchestra-Target). */
  targetHeader: string;
  /** Values for optional params the model leaves out. Its own argument wins. */
  defaults?: Record<string, string>;
  /** Absent => a text tool, the historical behaviour. */
  output?: ToolOutput;
}

/** Whether a tool writes a file rather than answering the model in text. */
export function producesArtifact(t: Pick<ToolDef, "output">): boolean {
  return t.output?.kind === "binary" || t.output?.kind === "base64";
}

/** JSON-schema `input_schema` advertised to the model, built from params. */
export function toInputSchema(params: ToolParam[]): Record<string, unknown> {
  const properties: Record<string, unknown> = {};
  const required: string[] = [];
  for (const p of params) {
    if (!p.name) continue;
    properties[p.name] = { type: p.type || "string", description: p.description };
    if (p.required) required.push(p.name);
  }
  const schema: Record<string, unknown> = { type: "object", properties };
  if (required.length) schema.required = required;
  return schema;
}

/** The agent-facing HTTP tool shape (matches the Go ToolDef / HTTPTool JSON). */
export interface CompiledTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  method: string;
  path: string;
  headers: Record<string, string>;
  body: string;
  targetHeader: string;
  defaults?: Record<string, string>;
  output?: ToolOutput;
}

/** Built-in RAG search tool (knowledge base via the gateway's /rag route). It is
 * auto-added to agents with useRag; the sandbox reaches the RAG service only
 * through the gateway, never directly.
 *
 * The two descriptions are getters rather than strings because this object is a
 * module-level constant: a plain string would freeze whichever language was
 * active at import time, while a getter is read when the tool is serialised for
 * a run — which is when the operator's current language is the right one. */
export const RAG_SEARCH_TOOL: CompiledTool = {
  name: "rag_search",
  get description() { return i18n.t("tools.ragSearch.description"); },
  inputSchema: {
    type: "object",
    properties: {
      query: { type: "string", get description() { return i18n.t("tools.ragSearch.query"); } },
    },
    required: ["query"],
  },
  method: "POST",
  path: "/rag/search",
  headers: {},
  body: '{"query":"{{query}}","k":5}',
  targetHeader: "",
};

export function compileTool(t: ToolDef): CompiledTool {
  return {
    name: t.name,
    description: t.description,
    inputSchema: toInputSchema(t.params),
    method: t.method || "POST",
    path: t.path,
    headers: t.headers ?? {},
    body: t.body ?? "",
    targetHeader: t.targetHeader ?? "",
    // Only carried when set: a text tool's wire shape is unchanged, so an
    // agent built before artifact outputs existed still sees what it saw.
    ...(t.defaults && Object.keys(t.defaults).length ? { defaults: t.defaults } : {}),
    ...(producesArtifact(t) ? { output: t.output } : {}),
  };
}
