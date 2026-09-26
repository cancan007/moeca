import { describe, expect, it } from "vitest";
import { markdownDocument, renderMarkdown } from "./markdown";

describe("renderMarkdown", () => {
  it("renders headings, emphasis and inline code", () => {
    const html = renderMarkdown("# Title\n\nsome **bold** and *thin* and `code()`");
    expect(html).toContain("<h1>Title</h1>");
    expect(html).toContain("<strong>bold</strong>");
    expect(html).toContain("<em>thin</em>");
    expect(html).toContain("<code>code()</code>");
  });

  it("joins wrapped lines into one paragraph and splits on a blank line", () => {
    const html = renderMarkdown("one\ntwo\n\nthree");
    expect(html).toContain("<p>one two</p>");
    expect(html).toContain("<p>three</p>");
  });

  // The file was written by an agent, so raw HTML is shown rather than run.
  it("escapes raw HTML instead of passing it through", () => {
    const html = renderMarkdown("<script>alert(1)</script>\n");
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });

  it("leaves markdown syntax inside a code span alone", () => {
    const html = renderMarkdown("use `**not bold**` here");
    expect(html).toContain("<code>**not bold**</code>");
    expect(html).not.toContain("<strong>");
  });

  it("takes fenced code verbatim, with its language as a class", () => {
    const html = renderMarkdown("```go\nif x { // **not bold**\n}\n```");
    expect(html).toContain('<pre class="lang-go">');
    expect(html).toContain("if x { // **not bold**");
    expect(html).not.toContain("<strong>");
  });

  it("nests lists by indent and switches marker type", () => {
    const html = renderMarkdown("- a\n  - b\n- c");
    expect(html).toBe("<ul>\n<li>a</li>\n<ul>\n<li>b</li>\n</ul>\n<li>c</li>\n</ul>");
    expect(renderMarkdown("1. one\n2. two")).toContain("<ol>");
  });

  it("renders a pipe table only when the separator row is present", () => {
    const table = renderMarkdown("| a | b |\n| --- | --- |\n| 1 | 2 |");
    expect(table).toContain("<th>a</th>");
    expect(table).toContain("<td>2</td>");
    // Without the rule row, pipes are prose — a shell pipeline stays text.
    const prose = renderMarkdown("run `ls | wc -l` to count");
    expect(prose).not.toContain("<table>");
  });

  it("keeps links and images, and drops targets that are not fetchable", () => {
    expect(renderMarkdown("[x](https://e.com)")).toContain('<a href="https://e.com"');
    expect(renderMarkdown("![alt](https://e.com/a.png)")).toContain('<img src="https://e.com/a.png" alt="alt">');
    const bad = renderMarkdown("[click](javascript:alert(1))");
    expect(bad).not.toContain("javascript:");
    expect(bad).toContain("click");
  });

  it("renders blockquotes and rules", () => {
    expect(renderMarkdown("> quoted\n> more")).toContain("<blockquote>quoted more</blockquote>");
    expect(renderMarkdown("---")).toContain("<hr>");
  });
});

describe("markdownDocument", () => {
  it("produces a standalone themed document", () => {
    const doc = markdownDocument("# hi", "dark");
    expect(doc.startsWith("<!doctype html>")).toBe(true);
    expect(doc).toContain("<h1>hi</h1>");
    // The frame cannot read the app's CSS variables, so colours are literals.
    expect(doc).toContain("#0b0d11");
    expect(doc).not.toContain("var(--");
  });

  // The card variant has to fit a document into ~96px, so it drops the margins
  // and the reading measure — but not the type size, because a legible first
  // line is the whole point of the thumbnail.
  it("compacts for a card thumbnail without shrinking the body text", () => {
    const card = markdownDocument("# Report\n\nbody", "dark", true);
    expect(card).toContain("padding: 11px 13px");
    expect(card).toContain("max-width: none");
    expect(card).toContain("overflow: hidden");
    expect(card).toContain("400 13.5px/1.75");
    const page = markdownDocument("# Report\n\nbody", "dark");
    expect(page).toContain("padding: 22px 28px");
    expect(page).toContain("max-width: 680px");
  });

  it("follows the app theme", () => {
    expect(markdownDocument("x", "light")).toContain("#ffffff");
    expect(markdownDocument("x", "light")).toContain("color-scheme: light");
  });
});
