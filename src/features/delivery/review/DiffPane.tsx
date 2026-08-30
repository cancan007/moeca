import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { DiffFile } from "@/lib/hostagent";

type Row =
  | { k: "hunk"; text: string }
  | { k: "ctx"; n: number; text: string }
  | { k: "add"; n: number; text: string }
  | { k: "del"; text: string };


const gutter: React.CSSProperties = { width: 44, textAlign: "right", paddingRight: 14, flex: "none" };

function DiffRows({ rows }: { rows: Row[] }) {
  return (
    <div style={{ fontFamily: "'IBM Plex Mono',monospace", fontSize: 11.5, lineHeight: 1.9 }}>
      {rows.map((r, i) => {
        if (r.k === "hunk")
          return (
            <div key={i} style={{ display: "flex", padding: "8px 0", background: "var(--diff-hunk)" }}>
              <span style={{ ...gutter, color: "var(--tx-gutter)" }}>@@</span>
              <span style={{ color: "#5b9fe8" }}>{r.text}</span>
            </div>
          );
        if (r.k === "ctx")
          return (
            <div key={i} style={{ display: "flex" }}>
              <span style={{ ...gutter, color: "var(--tx-gutter)" }}>{r.n}</span>
              <span style={{ flex: 1, color: "var(--tx3)", paddingLeft: 12 }}>{r.text}</span>
            </div>
          );
        if (r.k === "add")
          return (
            <div key={i} style={{ display: "flex", background: "var(--diff-add-bg)" }}>
              <span style={{ ...gutter, color: "var(--green)" }}>{r.n}</span>
              <span style={{ flex: 1, color: "#9fe0c2", paddingLeft: 12, borderLeft: "2px solid var(--green)" }}>{r.text}</span>
            </div>
          );
        return (
          <div key={i} style={{ display: "flex", background: "var(--diff-del-bg)" }}>
            <span style={{ ...gutter, color: "var(--red)" }}>—</span>
            <span style={{ flex: 1, color: "#e6a8a8", paddingLeft: 12, borderLeft: "2px solid var(--red)" }}>{r.text}</span>
          </div>
        );
      })}
    </div>
  );
}

function toRows(f: DiffFile): Row[] {
  // binary / rename-only files have no hunks and arrive without lines
  return (f.lines ?? []).map((l): Row => {
    switch (l.type) {
      case "hunk": return { k: "hunk", text: l.content };
      case "add": return { k: "add", n: l.newNo ?? 0, text: "+ " + l.content };
      case "del": return { k: "del", text: "- " + l.content };
      default: return { k: "ctx", n: l.oldNo ?? 0, text: "  " + l.content };
    }
  });
}

function FileTabs({ names, active, onPick, extra }: { names: string[]; active: number; onPick: (i: number) => void; extra?: string }) {
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 2, padding: "9px 16px 0", borderBottom: "1px solid var(--bd)", overflowX: "auto" }}>
      {names.map((name, i) => (
        <div key={name} onClick={() => onPick(i)} style={{ font: "500 11px 'IBM Plex Mono'", color: i === active ? "var(--tx)" : "var(--tx-dim)", padding: "7px 12px", borderBottom: i === active ? "2px solid var(--ac)" : "2px solid transparent", cursor: "pointer", whiteSpace: "nowrap" }}>
          {name.split("/").pop()}
        </div>
      ))}
      {extra && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-gutter)", padding: "7px 8px" }}>{extra}</div>}
    </div>
  );
}

/** DiffPane renders the host agent's diff for a task. `files` is undefined
 *  while it is still loading and empty when the branch has no changes — two
 *  different things, and the pane says which. */
export function DiffPane({ files }: { files?: DiffFile[] }) {
  const { t } = useTranslation();
  const [active, setActive] = useState(0);

  if (files && files.length > 0) {
    const idx = Math.min(active, files.length - 1);
    const f = files[idx];
    return (
      <>
        <FileTabs names={files.map((x) => x.path)} active={idx} onPick={setActive} />
        <DiffRows rows={toRows(f)} />
      </>
    );
  }

  const note = files ? t("review.noDiff") : t("common.loading");
  return <div style={{ padding: "40px 16px", textAlign: "center", font: "400 12px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{note}</div>;
}
