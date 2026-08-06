// Explorer tree — flattened rows rendered from the worktree file tree.

export type FlatRow =
  | { kind: "dir"; path: string; name: string; depth: number; open: boolean }
  | { kind: "file"; path: string; name: string; depth: number; active: boolean; dirty: boolean; color: string };

export function FileTree({
  root,
  rows,
  onToggleDir,
  onOpenFile,
}: {
  root: string;
  rows: FlatRow[];
  onToggleDir: (path: string) => void;
  onOpenFile: (path: string) => void;
}) {
  return (
    <div style={{ width: 248, flex: "none", background: "var(--bg-panel)", borderRight: "1px solid var(--bd)", display: "flex", flexDirection: "column", minHeight: 0 }}>
      <div style={{ flex: "none", padding: "11px 14px 8px", font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px", textTransform: "uppercase" }}>
        Explorer — {root}
      </div>
      <div style={{ flex: 1, overflowY: "auto", padding: "2px 6px 12px" }}>
        {rows.map((n) => {
          const rowStyle = {
            display: "flex",
            alignItems: "center",
            gap: 6,
            padding: "4px 8px",
            paddingLeft: 8 + n.depth * 14,
            borderRadius: 6,
            cursor: "pointer",
            font: "400 12px 'IBM Plex Mono'",
            color: n.kind === "file" && n.active ? "var(--tx)" : "var(--tx3)",
            background: n.kind === "file" && n.active ? "var(--tint-active)" : "transparent",
          } as const;

          if (n.kind === "dir") {
            return (
              <div key={n.path} onClick={() => onToggleDir(n.path)} style={rowStyle}>
                <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-dim)", width: 10 }}>{n.open ? "▾" : "▸"}</span>
                <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="var(--tx-dim)" strokeWidth="1.4">
                  <path d="M2 4.5h4l1 1.5h7v6H2z" />
                </svg>
                <span>{n.name}</span>
              </div>
            );
          }
          return (
            <div key={n.path} onClick={() => onOpenFile(n.path)} style={rowStyle}>
              <div style={{ width: 10, flex: "none", display: "flex", justifyContent: "center" }}>
                <div style={{ width: 7, height: 7, borderRadius: 2, background: n.color, flex: "none" }} />
              </div>
              <span>{n.name}</span>
              {n.dirty && <span style={{ marginLeft: "auto", color: "#d39a4e", fontSize: 10 }}>●</span>}
            </div>
          );
        })}
      </div>
    </div>
  );
}
