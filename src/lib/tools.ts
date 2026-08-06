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
  };
}
