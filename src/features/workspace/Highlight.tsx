// Lightweight syntax highlighter for the workspace's highlight view.
// Renders each source line with a monospace line-number gutter (DiffPane pattern).

interface Seg {
  t: string;
  c: string;
}

const KEYWORDS = new Set([
  "async", "await", "function", "const", "let", "var", "return", "for", "if", "else", "while",
  "export", "import", "from", "new", "class", "extends", "this", "self", "true", "false", "null",
  "undefined", "typeof", "in", "of",
  // SQL
  "CREATE", "TABLE", "INDEX", "PRIMARY", "KEY", "NOT", "DEFAULT", "ON", "BIGINT", "TEXT", "BOOLEAN",
  "SELECT", "COMMIT", "FROM",
  // test globals
  "test", "expect",
]);

const COLOR = {
  keyword: "#5b9fe8",
  string: "#9fe0c2",
  number: "#d39a4e",
  comment: "var(--tx-gutter)",
  func: "#34d3e0",
  ident: "var(--tx2)",
  punct: "var(--tx-dim)",
};

const TOKEN_RE = /(\/\/.*$|#.*$)|(`[^`]*`|"[^"]*"|'[^']*')|(\d+(?:\.\d+)?)|([A-Za-z_$][A-Za-z0-9_$]*)|(\s+)|([^\s])/g;

function tokenize(line: string): Seg[] {
  const segs: Seg[] = [];
  let m: RegExpExecArray | null;
  TOKEN_RE.lastIndex = 0;
  while ((m = TOKEN_RE.exec(line)) !== null) {
    if (m[1] !== undefined) segs.push({ t: m[1], c: COLOR.comment });
    else if (m[2] !== undefined) segs.push({ t: m[2], c: COLOR.string });
    else if (m[3] !== undefined) segs.push({ t: m[3], c: COLOR.number });
    else if (m[4] !== undefined) {
      const rest = line.slice(TOKEN_RE.lastIndex);
      const isCall = /^\s*\(/.test(rest);
      const c = KEYWORDS.has(m[4]) ? COLOR.keyword : isCall ? COLOR.func : COLOR.ident;
      segs.push({ t: m[4], c });
    } else if (m[5] !== undefined) segs.push({ t: m[5], c: COLOR.ident });
    else if (m[6] !== undefined) segs.push({ t: m[6], c: COLOR.punct });
  }
  return segs;
}

export function Highlight({ code, plain }: { code: string; plain?: boolean }) {
  const lines = code.split("\n");
  return (
    <div style={{ fontFamily: "'IBM Plex Mono',monospace", fontSize: 12.5, lineHeight: 1.9 }}>
      {lines.map((ln, i) => (
        <div key={i} style={{ display: "flex" }}>
          <span style={{ width: 38, flex: "none", textAlign: "right", paddingRight: 16, color: "var(--tx-gutter)", userSelect: "none" }}>{i + 1}</span>
          <span style={{ flex: 1, whiteSpace: "pre", color: "var(--tx2)" }}>
            {plain
              ? ln
              : tokenize(ln).map((s, j) => (
                  <span key={j} style={{ color: s.c }}>{s.t}</span>
                ))}
          </span>
        </div>
      ))}
    </div>
  );
}
