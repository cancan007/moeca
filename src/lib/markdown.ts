// Markdown → HTML for artifact previews.
//
// Hand-written rather than a dependency, for two reasons. The app ships as a
// desktop bundle whose whole claim is that local data stays local, so every
// dependency is supply-chain surface it has to justify; and correctness here is
// not load-bearing for safety, because the output is only ever rendered inside a
// sandboxed iframe (see ArtifactGallery). A renderer bug shows the wrong
// formatting; it cannot reach the app.
//
// Raw HTML in the source is ESCAPED, not passed through. GitHub renders it
// (behind a sanitizer); this does not, because the text was written by an agent
// and showing it literally is the predictable outcome — an artifact that wants
// to be a web page can be written as one, and .html has its own preview.

const ESCAPES: Record<string, string> = {
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
};

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ESCAPES[c]);
}

/** Link and image targets are limited to schemes that only fetch or navigate.
 *  Scripts cannot run in the preview frame at all, so this is hygiene rather
 *  than the boundary: it keeps a `javascript:` href from rendering as something
 *  a reader might later copy somewhere that would run it. */
function safeUrl(raw: string): string | null {
  const u = raw.trim();
  if (/^(https?:\/\/|mailto:|data:image\/)/i.test(u)) return u;
  return null;
}

/** Inline spans, applied to one already-block-classified piece of text.
 *
 *  Code spans are lifted out FIRST and put back last, so their contents are
 *  never touched by the emphasis or link rules — `**not bold**` inside
 *  backticks has to survive as typed. */
function inline(src: string): string {
  const code: string[] = [];
  let s = src.replace(/`([^`]+)`/g, (_m, c: string) => {
    code.push(`<code>${escapeHtml(c)}</code>`);
    return `\u0000${code.length - 1}\u0000`;
  });

  s = escapeHtml(s);

  // Images before links: the two syntaxes differ only by the leading "!".
  s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (_m, alt: string, url: string) => {
    const u = safeUrl(url);
    return u ? `<img src="${u}" alt="${alt}">` : alt;
  });
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, text: string, url: string) => {
    const u = safeUrl(url);
    return u ? `<a href="${u}" target="_blank" rel="noreferrer">${text}</a>` : text;
  });

  s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, "$1<em>$2</em>");
  s = s.replace(/(^|\W)_([^_\n]+)_(?=\W|$)/g, "$1<em>$2</em>");
  s = s.replace(/~~([^~]+)~~/g, "<del>$1</del>");

  return s.replace(/\u0000(\d+)\u0000/g, (_m, i: string) => code[Number(i)]);
}

/** One row of a pipe table, minus the outer pipes. */
function cells(line: string): string[] {
  return line.replace(/^\s*\|/, "").replace(/\|\s*$/, "").split("|").map((c) => c.trim());
}

const isTableRule = (line: string) => /^\s*\|?[\s:|-]+\|[\s:|-]*$/.test(line) && line.includes("-");

/** renderMarkdown returns a BODY fragment — no <html>, no styles. The caller
 *  decides how it is framed (markdownDocument below is the usual answer). */
export function renderMarkdown(src: string): string {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: string[] = [];
  // Open list levels, outermost first, so nesting closes in order.
  const lists: { tag: "ul" | "ol"; indent: number }[] = [];
  let para: string[] = [];

  const closeLists = (toIndent = -1) => {
    while (lists.length && lists[lists.length - 1].indent > toIndent) {
      out.push(`</${lists.pop()!.tag}>`);
    }
  };
  const flushPara = () => {
    if (para.length) {
      out.push(`<p>${inline(para.join(" "))}</p>`);
      para = [];
    }
  };
  const breakBlock = () => { flushPara(); closeLists(); };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    // Fenced code: taken verbatim to the closing fence, which is why it is
    // checked before anything that could match inside it.
    const fence = /^\s*(```|~~~)(.*)$/.exec(line);
    if (fence) {
      breakBlock();
      const marker = fence[1];
      const lang = fence[2].trim().split(/\s+/)[0];
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trimStart().startsWith(marker)) {
        body.push(lines[i]);
        i++;
      }
      const cls = lang ? ` class="lang-${escapeHtml(lang)}"` : "";
      out.push(`<pre${cls}><code>${escapeHtml(body.join("\n"))}</code></pre>`);
      continue;
    }

    if (!line.trim()) { breakBlock(); continue; }

    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      breakBlock();
      const level = heading[1].length;
      out.push(`<h${level}>${inline(heading[2].trim())}</h${level}>`);
      continue;
    }

    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      breakBlock();
      out.push("<hr>");
      continue;
    }

    const quote = /^\s*>\s?(.*)$/.exec(line);
    if (quote) {
      breakBlock();
      // Consecutive quote lines join into one blockquote.
      const body = [quote[1]];
      while (i + 1 < lines.length) {
        const next = /^\s*>\s?(.*)$/.exec(lines[i + 1]);
        if (!next) break;
        body.push(next[1]);
        i++;
      }
      out.push(`<blockquote>${inline(body.join(" "))}</blockquote>`);
      continue;
    }

    // A table needs its separator row to be a table at all; without it the
    // pipes are just text, which is how a shell command in prose stays prose.
    if (line.includes("|") && i + 1 < lines.length && isTableRule(lines[i + 1])) {
      breakBlock();
      const head = cells(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim()) {
        rows.push(cells(lines[i]));
        i++;
      }
      i--;
      const th = head.map((c) => `<th>${inline(c)}</th>`).join("");
      const tb = rows
        .map((r) => `<tr>${r.map((c) => `<td>${inline(c)}</td>`).join("")}</tr>`)
        .join("");
      out.push(`<table><thead><tr>${th}</tr></thead><tbody>${tb}</tbody></table>`);
      continue;
    }

    const item = /^(\s*)([-*+]|\d+[.)])\s+(.*)$/.exec(line);
    if (item) {
      flushPara();
      const indent = item[1].length;
      const tag: "ul" | "ol" = /\d/.test(item[2]) ? "ol" : "ul";
      closeLists(indent);
      const top = lists[lists.length - 1];
      if (!top || top.indent < indent) {
        lists.push({ tag, indent });
        out.push(`<${tag}>`);
      } else if (top.tag !== tag) {
        out.push(`</${top.tag}>`);
        lists[lists.length - 1] = { tag, indent };
        out.push(`<${tag}>`);
      }
      out.push(`<li>${inline(item[3])}</li>`);
      continue;
    }

    // Anything else is prose. Lines accumulate so a paragraph wrapped across
    // several source lines renders as one.
    closeLists();
    para.push(line.trim());
  }

  breakBlock();
  return out.join("\n");
}

/** Colours for the preview frame. The frame is a separate document, so it
 *  cannot read the app's CSS variables — the two themes are passed in as
 *  literals instead, which is also why they are kept next to each other here
 *  rather than spread through a stylesheet. Values match src/styles/index.css. */
const PALETTE = {
  dark: { bg: "#0b0d11", tx: "#e6e9ef", tx2: "#c3cad6", dim: "#8b93a3", bd: "#242a34", inset: "#0f1418", ac: "#4f9dff" },
  light: { bg: "#ffffff", tx: "#1b2331", tx2: "#36414f", dim: "#6c7682", bd: "#dce1e9", inset: "#f3f5f9", ac: "#4f9dff" },
};

/** markdownDocument wraps rendered markdown in a standalone HTML document for
 *  the preview frame, themed to match the app.
 *
 *  compact is the card-thumbnail variant: same document, but the margins and the
 *  reading measure are dropped so the ~96px on show is text rather than padding,
 *  and it never scrolls. Text stays at full size deliberately — a card is worth
 *  more when its first line is legible than when the whole page is visible and
 *  none of it can be read. */
export function markdownDocument(src: string, theme: "dark" | "light", compact = false): string {
  const c = PALETTE[theme === "light" ? "light" : "dark"];
  return `<!doctype html>
<html><head><meta charset="utf-8"><style>
  :root { color-scheme: ${theme}; }
  body { margin: 0; padding: ${compact ? "11px 13px" : "22px 28px"}; background: ${c.bg}; color: ${c.tx2};
    font: 400 13.5px/1.75 "IBM Plex Sans", system-ui, -apple-system, "Hiragino Kaku Gothic ProN", "Yu Gothic", Meiryo, sans-serif;
    -webkit-font-smoothing: antialiased; overflow-wrap: break-word;
    ${compact ? "overflow: hidden;" : ""} }
  /* Centred rather than left-aligned: the column is a reading measure, and in a
     full-screen preview a left-pinned one leaves the page looking broken. */
  .doc { max-width: ${compact ? "none" : "680px"}; margin: 0 auto; }
  h1, h2, h3, h4, h5, h6 { color: ${c.tx}; line-height: 1.35; margin: 26px 0 10px; font-weight: 600; }
  h1 { font-size: ${compact ? "17px" : "22px"}; font-weight: 700; margin-top: 0; letter-spacing: -0.3px; }
  ${compact ? ".doc > :first-child { margin-top: 0; }" : ""}
  h2 { font-size: 16.5px; } h3 { font-size: 14.5px; } h4, h5, h6 { font-size: 13.5px; }
  p { margin: 0 0 12px; }
  ul, ol { margin: 0 0 12px; padding-left: 22px; }
  li { margin: 3px 0; }
  li > ul, li > ol { margin: 3px 0; }
  a { color: ${c.ac}; text-decoration: none; }
  a:hover { text-decoration: underline; }
  strong { color: ${c.tx}; font-weight: 600; }
  code { font: 500 11.5px "IBM Plex Mono", ui-monospace, Menlo, monospace;
    background: ${c.inset}; border: 1px solid ${c.bd}; border-radius: 4px; padding: 1px 5px; }
  pre { background: ${c.inset}; border: 1px solid ${c.bd}; border-radius: 8px;
    padding: 12px 14px; overflow-x: auto; margin: 0 0 14px; }
  pre code { background: none; border: 0; padding: 0; font-size: 11.5px; line-height: 1.6; }
  blockquote { margin: 0 0 14px; padding: 2px 0 2px 14px; border-left: 2px solid ${c.bd}; color: ${c.dim}; }
  hr { border: 0; border-top: 1px solid ${c.bd}; margin: 22px 0; }
  table { border-collapse: collapse; margin: 0 0 14px; font-size: 12.5px; display: block; overflow-x: auto; }
  th, td { border: 1px solid ${c.bd}; padding: 6px 10px; text-align: left; vertical-align: top; }
  th { color: ${c.tx}; background: ${c.inset}; font-weight: 600; }
  img { max-width: 100%; height: auto; border-radius: 6px; }
  del { color: ${c.dim}; }
</style></head>
<body><div class="doc">${renderMarkdown(src)}</div></body></html>`;
}
