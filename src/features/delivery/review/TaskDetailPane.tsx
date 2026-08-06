import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { hostagent } from "@/lib/hostagent";

// The task doc lives in the worktree; the agent reads it as its task, and this
// pane views/edits it as markdown.
const TASK_PATH = ".orchestra/task.md";

const codeInline: React.CSSProperties = { font: "500 11.5px 'IBM Plex Mono'", background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 4, padding: "1px 5px", color: "var(--tx2)" };
const preStyle: React.CSSProperties = { font: "500 11.5px 'IBM Plex Mono'", background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "11px 13px", color: "var(--tx2)", lineHeight: 1.6, overflowX: "auto", margin: "6px 0" };

// inline: **bold** and `code`.
function inline(text: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  const re = /(\*\*([^*]+)\*\*|`([^`]+)`)/g;
  let last = 0, m: RegExpExecArray | null, i = 0;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) nodes.push(text.slice(last, m.index));
    if (m[2] !== undefined) nodes.push(<strong key={i++} style={{ color: "var(--tx)" }}>{m[2]}</strong>);
    else if (m[3] !== undefined) nodes.push(<code key={i++} style={codeInline}>{m[3]}</code>);
    last = m.index + m[0].length;
  }
  if (last < text.length) nodes.push(text.slice(last));
  return nodes;
}

// Markdown — a small renderer covering headings, bold, inline code, fenced code
// blocks and unordered lists (enough for the task docs Orchestra writes). HTML
// comments (our template marker) are skipped.
function Markdown({ text }: { text: string }) {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const blocks: React.ReactNode[] = [];
  let i = 0, key = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.startsWith("```")) {
      const buf: string[] = []; i++;
      while (i < lines.length && !lines[i].startsWith("```")) { buf.push(lines[i]); i++; }
      i++;
      blocks.push(<pre key={key++} style={preStyle}>{buf.join("\n")}</pre>);
      continue;
    }
    if (/^#{1,3}\s/.test(line)) {
      const level = line.match(/^#+/)![0].length;
      const size = level === 1 ? 17 : level === 2 ? 14.5 : 12.5;
      blocks.push(<div key={key++} style={{ font: `700 ${size}px 'IBM Plex Sans'`, color: "var(--tx)", margin: "14px 0 6px" }}>{inline(line.replace(/^#+\s/, ""))}</div>);
      i++; continue;
    }
    if (/^\s*[-*]\s/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[-*]\s/.test(lines[i])) { items.push(lines[i].replace(/^\s*[-*]\s/, "")); i++; }
      blocks.push(
        <ul key={key++} style={{ margin: "4px 0 8px", paddingLeft: 20, display: "flex", flexDirection: "column", gap: 3 }}>
          {items.map((it, j) => <li key={j} style={{ font: "400 12.5px 'IBM Plex Sans'", color: "var(--tx2)", lineHeight: 1.6 }}>{inline(it)}</li>)}
        </ul>,
      );
      continue;
    }
    if (line.trim() === "" || line.trim().startsWith("<!--")) { i++; continue; }
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== "" && !/^#{1,3}\s/.test(lines[i]) && !/^\s*[-*]\s/.test(lines[i]) && !lines[i].startsWith("```") && !lines[i].trim().startsWith("<!--")) {
      para.push(lines[i]); i++;
    }
    blocks.push(<p key={key++} style={{ font: "400 12.5px 'IBM Plex Sans'", color: "var(--tx2)", lineHeight: 1.65, margin: "0 0 8px" }}>{inline(para.join(" "))}</p>);
  }
  return <div>{blocks}</div>;
}

// TaskDetailPane views and edits the worktree's .orchestra/task.md — the task
// description (and the agent's instruction). Missing file is treated as empty.
export function TaskDetailPane({ repo, branch, live }: { repo: string; branch: string; live: boolean }) {
  const { t } = useTranslation();
  const [text, setText] = useState("");
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!live) { setText(""); return; }
    setEditing(false);
    hostagent.file(repo, branch, TASK_PATH)
      .then((c) => setText(c))
      .catch(() => setText("")); // no task.md yet
  }, [repo, branch, live]);

  if (!live) {
    return <div style={{ padding: 22, font: "400 12px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("review.connectToView")}</div>;
  }

  const save = async () => {
    setBusy(true); setErr(null);
    try {
      await hostagent.writeFile(repo, branch, TASK_PATH, draft);
      setText(draft);
      setEditing(false);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "9px 16px", borderBottom: "1px solid var(--bd)" }}>
        <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px" }}>.orchestra/task.md</span>
        {err && <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</span>}
        <div style={{ flex: 1 }} />
        {editing ? (
          <>
            <div onClick={() => setEditing(false)} style={btn}>{t("daily.preview")}</div>
            <div onClick={() => !busy && save()} style={{ ...btn, color: "#06121e", background: "var(--ac)", border: "none" }}>{busy ? t("review.saving") : t("common.save")}</div>
          </>
        ) : (
          <div onClick={() => { setDraft(text); setEditing(true); }} style={btn}>{t("common.edit")}</div>
        )}
      </div>
      {editing ? (
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          spellCheck={false}
          style={{ width: "100%", minHeight: 340, boxSizing: "border-box", resize: "vertical", background: "var(--bg-deep)", border: "none", outline: "none", padding: "16px 20px", font: "400 12px 'IBM Plex Mono'", lineHeight: 1.7, color: "var(--tx2)" }}
        />
      ) : (
        <div style={{ padding: "16px 20px" }}>
          {text.trim() ? (
            <Markdown text={text} />
          ) : (
            <div style={{ font: "400 12px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("review.noTaskDetail")}</div>
          )}
        </div>
      )}
    </div>
  );
}

const btn: React.CSSProperties = { font: "600 10px 'IBM Plex Sans'", color: "var(--tx3)", cursor: "pointer", padding: "5px 11px", border: "1px solid var(--bd2)", borderRadius: 6 };
