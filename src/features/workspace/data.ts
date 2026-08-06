// Mock worktree data for the VSCode-like workspace screen.
import i18n from "@/i18n";
// Ported from the `workspaces` map in design/Orchestra.dc.html (task t3).

export interface FileNode {
  type: "dir" | "file";
  path: string;
  children?: FileNode[];
}

export interface WorkspaceData {
  root: string;
  main: string;
  jira: string;
  taskTitle: string;
  tree: FileNode[];
  files: Record<string, string>;
}

export interface RefItem {
  name: string;
  kind: string;
  def: string;
  use: string;
}

export const workspace: WorkspaceData = {
  root: "wt/fix-index",
  main: "src/indexer.ts",
  jira: "SRCH-42",
  taskTitle: "検索インデックスの再構築",
  tree: [
    {
      type: "dir",
      path: "src",
      children: [
        { type: "file", path: "src/indexer.ts" },
        { type: "file", path: "src/worker.ts" },
        {
          type: "dir",
          path: "src/db",
          children: [
            { type: "file", path: "src/db/schema.sql" },
            { type: "file", path: "src/db/client.ts" },
          ],
        },
      ],
    },
    {
      type: "dir",
      path: "test",
      children: [{ type: "file", path: "test/indexer.test.ts" }],
    },
    { type: "file", path: "package.json" },
    { type: "file", path: "README.md" },
  ],
  files: {
    "src/indexer.ts":
      "async function rebuildIndex() {\n  const docs = await db.fetchAll();\n  const batched = chunk(docs, 500);\n  for (const part of batched) {\n    await index.bulk(part);\n  }\n  await db.commit();\n}\n\nexport function chunk(a, n) {\n  const out = [];\n  for (let i = 0; i < a.length; i += n) out.push(a.slice(i, i + n));\n  return out;\n}",
    "src/worker.ts":
      "import { rebuildIndex } from './indexer';\n\nself.onmessage = async (e) => {\n  if (e.data.type === 'rebuild') {\n    await rebuildIndex();\n    self.postMessage({ done: true });\n  }\n};",
    "src/db/schema.sql":
      "CREATE TABLE documents (\n  id      BIGINT PRIMARY KEY,\n  body    TEXT NOT NULL,\n  indexed BOOLEAN DEFAULT false\n);\n\nCREATE INDEX idx_documents_indexed ON documents(indexed);",
    "src/db/client.ts":
      "export const db = {\n  fetchAll: () => query('SELECT * FROM documents'),\n  commit: () => query('COMMIT'),\n};",
    "test/indexer.test.ts":
      "import { chunk } from '../src/indexer';\n\ntest('chunk splits evenly', () => {\n  expect(chunk([1,2,3,4,5], 2)).toEqual([[1,2],[3,4],[5]]);\n});",
    "package.json":
      '{\n  "name": "search-indexer",\n  "version": "2.4.0",\n  "scripts": {\n    "test": "vitest"\n  }\n}',
    "README.md":
      "# Search Indexer\n\nバッチ処理で全文インデックスを再構築するワーカー。\n\n- `rebuildIndex()` — 500件ずつ bulk 投入\n- worktree: wt/fix-index",
  },
};

// Symbol references per file (for the REFERENCES side panel).
export const refsByPath: Record<string, RefItem[]> = {
  "src/indexer.ts": [
    { name: "rebuildIndex", kind: "function", def: "定義 src/indexer.ts:1", use: "参照 worker.ts で 1 件" },
    { name: "chunk", kind: "function", def: "定義 src/indexer.ts:11", use: "参照 test/indexer.test.ts で 1 件" },
  ],
  "src/db/client.ts": [
    { name: "db", kind: "const", def: "定義 src/db/client.ts:1", use: "参照 indexer.ts で 3 件" },
  ],
  "src/worker.ts": [],
  "src/db/schema.sql": [],
  "test/indexer.test.ts": [],
  "package.json": [],
  "README.md": [],
};

export function baseName(p: string): string {
  return p.split("/").pop() ?? p;
}

// File-type dot color — ported from fileIcon() in the DC logic.
export function fileIcon(path: string): string {
  const ext = path.split(".").pop() ?? "";
  const map: Record<string, string> = { ts: "#5b9fe8", tsx: "#34d3e0", sql: "#d39a4e", json: "#e0a83e", md: "#67c9a4" };
  return map[ext] ?? "var(--tx-dim)";
}

export function langOf(path: string): string {
  const ext = path.split(".").pop() ?? "";
  const map: Record<string, string> = {
    ts: "TypeScript",
    tsx: "TypeScript React",
    sql: "SQL",
    json: "JSON",
    md: "Markdown",
    js: "JavaScript",
  };
  return map[ext] ?? "Plain Text";
}

export interface PluginBadge {
  label: string;
  color: string;
  bg: string;
  bd: string;
}

// Language-server / plugin pill shown in the editor toolbar.
export function pluginBadge(path: string): PluginBadge {
  const ext = path.split(".").pop() ?? "";
  if (ext === "ts" || ext === "tsx") return { label: i18n.t("workspace.lspRunning", { name: "tsserver" }), color: "#3fbf8f", bg: "var(--tint-green)", bd: "var(--tint-green-bd)" };
  if (ext === "sql") return { label: i18n.t("workspace.lspRunning", { name: "sqls" }), color: "#d39a4e", bg: "var(--tint-amber)", bd: "var(--bd2)" };
  return { label: i18n.t("workspace.noLsp"), color: "var(--tx-dim)", bg: "var(--bg-card2)", bd: "var(--bd2)" };
}

// Extract imported module paths from source (for the IMPORTS section).
export function importsOf(content: string): string[] {
  const out: string[] = [];
  const re = /from\s+['"]([^'"]+)['"]|import\s+['"]([^'"]+)['"]/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(content)) !== null) {
    const p = m[1] ?? m[2];
    if (p) out.push(p);
  }
  return out;
}
