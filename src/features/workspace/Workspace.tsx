// WORKSPACE — VSCode-like worktree editor.
import { useTranslation } from "react-i18next";
// Ported from design/Orchestra.dc.html (<!-- WORKSPACE --> section).
// Opened from Delivery's "open in Workspace"; edits happen inside the sandbox-isolated worktree.
// When the reviewed task is a live host-agent worktree, the tree and file
// contents come from hostagent and edits are persisted back to disk.

import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { FileTree, type FlatRow } from "./FileTree";
import { EditorPane } from "./EditorPane";
import { workspace, baseName, fileIcon } from "./data";
import type { FileNode } from "./data";
import { useStore } from "@/store/useStore";
import { hostagent } from "@/lib/hostagent";

type View = "hl" | "edit";

// Build a nested FileNode tree from a flat list of "a/b/c.ts" paths.
function buildTree(paths: string[]): FileNode[] {
  const roots: FileNode[] = [];
  const dirs = new Map<string, FileNode>();
  for (const p of [...paths].sort()) {
    const parts = p.split("/");
    let prefix = "";
    let siblings = roots;
    for (let i = 0; i < parts.length; i++) {
      prefix = prefix ? `${prefix}/${parts[i]}` : parts[i];
      const isFile = i === parts.length - 1;
      if (isFile) {
        siblings.push({ type: "file", path: prefix });
      } else {
        let dir = dirs.get(prefix);
        if (!dir) {
          dir = { type: "dir", path: prefix, children: [] };
          dirs.set(prefix, dir);
          siblings.push(dir);
        }
        siblings = dir.children!;
      }
    }
  }
  return roots;
}

export function Workspace() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const task = useStore((s) => s.tasks.find((t) => t.id === s.reviewId));
  const refreshLive = useStore((s) => s.refreshLive);
  const live = !!task?.live;
  const repo = task?.project ?? "";
  const branch = task?.branch ?? "";

  // Tree + file content: mock uses the static `workspace`; live loads from hostagent.
  const [liveTree, setLiveTree] = useState<FileNode[]>([]);
  const [cache, setCache] = useState<Record<string, string>>(live ? {} : workspace.files);
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const tree = live ? liveTree : workspace.tree;
  const root = live ? task!.worktree : workspace.root;

  const [active, setActive] = useState<string | null>(live ? null : workspace.main);
  const [tabs, setTabs] = useState<string[]>(live ? [] : [workspace.main]);
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [view, setView] = useState<View>("hl");
  const [refsOpen, setRefsOpen] = useState(true);
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState<Record<string, boolean>>({});

  // Load the live file tree; expand every directory by default.
  useEffect(() => {
    if (!live) return;
    let cancelled = false;
    (async () => {
      try {
        const files = await hostagent.files(repo, branch);
        if (cancelled) return;
        const t = buildTree(files);
        setLiveTree(t);
        const exp: Record<string, boolean> = {};
        const walk = (nodes: FileNode[]) => nodes.forEach((n) => { if (n.type === "dir") { exp[n.path] = true; if (n.children) walk(n.children); } });
        walk(t);
        setExpanded(exp);
        const first = files.find((f) => /\.(ts|tsx|js|go|py|rs|md)$/.test(f)) ?? files[0];
        if (first) void openLive(first);
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [live, repo, branch]);

  // Mock: expand directories once on mount.
  useEffect(() => {
    if (live) return;
    const exp: Record<string, boolean> = {};
    const walk = (nodes: FileNode[]) => nodes.forEach((n) => { if (n.type === "dir") { exp[n.path] = true; if (n.children) walk(n.children); } });
    walk(workspace.tree);
    setExpanded(exp);
  }, [live]);

  const openLive = async (path: string) => {
    setActive(path);
    setTabs((t) => (t.includes(path) ? t : t.concat([path])));
    if (cache[path] !== undefined) return;
    try {
      const content = await hostagent.file(repo, branch, path);
      setCache((m) => ({ ...m, [path]: content }));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  const content = useMemo(() => {
    if (!active) return `// ${t("workspace.noFileSelected")}`;
    return edits[active] ?? cache[active] ?? `// ${t("common.loading")}`;
  }, [active, edits, cache]);

  // Flatten the tree into visible rows honoring expand/collapse.
  const rows: FlatRow[] = [];
  const flatten = (nodes: FileNode[], depth: number) => {
    nodes.forEach((n) => {
      const name = baseName(n.path);
      if (n.type === "dir") {
        const open = !!expanded[n.path];
        rows.push({ kind: "dir", path: n.path, name, depth, open });
        if (open && n.children) flatten(n.children, depth + 1);
      } else {
        rows.push({ kind: "file", path: n.path, name, depth, active: active === n.path, dirty: !!dirty[n.path], color: fileIcon(n.path) });
      }
    });
  };
  flatten(tree, 0);

  const openFile = (path: string) => {
    if (live) { void openLive(path); return; }
    setActive(path);
    setTabs((t) => (t.includes(path) ? t : t.concat([path])));
  };

  const closeTab = (path: string) => {
    setTabs((t) => {
      const next = t.filter((x) => x !== path);
      setActive((cur) => (cur === path ? (next.length ? next[next.length - 1] : null) : cur));
      return next;
    });
  };

  const toggleDir = (path: string) => setExpanded((e) => ({ ...e, [path]: !e[path] }));

  const onEdit = (value: string) => {
    if (!active) return;
    const orig = cache[active];
    setEdits((m) => ({ ...m, [active]: value }));
    setDirty((m) => ({ ...m, [active]: value !== orig }));
  };

  const onSave = async () => {
    if (!live || !active || !dirty[active]) return;
    setSaving(true); setErr(null);
    try {
      const value = edits[active] ?? cache[active] ?? "";
      await hostagent.writeFile(repo, branch, active, value);
      setCache((m) => ({ ...m, [active]: value }));
      setEdits((m) => { const n = { ...m }; delete n[active]; return n; });
      setDirty((m) => ({ ...m, [active]: false }));
      await refreshLive(); // manual edit resets the CI gate
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  const jira = live ? task!.id : workspace.jira;
  const taskTitle = live ? task!.title : workspace.taskTitle;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, background: "var(--bg-app)" }}>
      {/* title bar */}
      <div style={{ flex: "none", height: 46, background: "var(--bg-panel)", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", padding: "0 14px", gap: 11 }}>
        <svg width="15" height="15" viewBox="0 0 16 16" fill="none" stroke="var(--ac)" strokeWidth="1.5">
          <rect x="2" y="3" width="12" height="10" rx="1.5" />
          <path d="M2 6h12M5 3v10" />
        </svg>
        <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("workspace.title")}</span>
        <span style={{ font: "500 10.5px 'IBM Plex Mono'", color: "var(--tx-faint)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 8px", borderRadius: 5 }}>{root}</span>
        <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{jira} · {taskTitle}</span>
        <div style={{ flex: 1 }} />
        {err && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</span>}
        <span style={{ display: "flex", alignItems: "center", gap: 6, font: "500 10px 'IBM Plex Mono'", color: "#67c9a4", background: "var(--tint-green)", border: "1px solid var(--tint-green-bd)", padding: "3px 9px", borderRadius: 6 }}>
          <span className="oc-active-dot" style={{ width: 6, height: 6, borderRadius: "50%", background: "#3fbf8f" }} />
          {t(live ? "workspace.editWorktree" : "workspace.editSandbox")}
        </span>
        <div onClick={() => navigate("/delivery")} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 19px 'IBM Plex Sans'", padding: "0 6px" }}>✕</div>
      </div>

      {/* body: explorer + editor */}
      <div style={{ flex: 1, display: "flex", minHeight: 0 }}>
        <FileTree root={root} rows={rows} onToggleDir={toggleDir} onOpenFile={openFile} />
        <EditorPane
          root={root}
          tabs={tabs}
          active={active}
          dirty={dirty}
          content={content}
          view={view}
          refsOpen={refsOpen}
          onSelectTab={setActive}
          onCloseTab={closeTab}
          onSetView={setView}
          onToggleRefs={() => setRefsOpen((v) => !v)}
          onEdit={onEdit}
          onSave={live ? onSave : undefined}
          saving={saving}
        />
      </div>
    </div>
  );
}
