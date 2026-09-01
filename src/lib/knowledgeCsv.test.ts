import { describe, expect, it } from "vitest";
import { parseSourcesCsv, planSync, toSourcesCsv } from "./knowledgeCsv";

describe("parseSourcesCsv", () => {
  it("reads kind,path with a header", () => {
    const { rows, problems } = parseSourcesCsv("kind,path\nlocal,/Users/me/docs\nexternal,https://example.com/a.md\n");
    expect(problems).toEqual([]);
    expect(rows).toEqual([
      { line: 2, kind: "local", path: "/Users/me/docs" },
      { line: 3, kind: "external", path: "https://example.com/a.md" },
    ]);
  });

  // A file that starts straight into data must not lose its first row, so the
  // header is detected by what it says rather than by being first.
  it("reads a file with no header", () => {
    const { rows } = parseSourcesCsv("local,/a\nlocal,/b\n");
    expect(rows.map((r) => r.path)).toEqual(["/a", "/b"]);
  });

  // The two kinds differ by exactly this rule, so making someone write it out
  // adds a column of noise and a column of typos.
  it("infers the kind from a single column", () => {
    const { rows } = parseSourcesCsv("/Users/me/docs\nhttps://example.com/a.md\n");
    expect(rows.map((r) => [r.kind, r.path])).toEqual([
      ["local", "/Users/me/docs"],
      ["external", "https://example.com/a.md"],
    ]);
  });

  // The case this feature exists for: a folder whose name has a comma in it.
  it("honours quoted fields", () => {
    const { rows } = parseSourcesCsv('kind,path\nlocal,"/Users/me/a,b/docs"\n');
    expect(rows[0].path).toBe("/Users/me/a,b/docs");
  });

  it("reads a doubled quote as one quote", () => {
    const { rows } = parseSourcesCsv('local,"/Users/me/say ""hi"""\n');
    expect(rows[0].path).toBe('/Users/me/say "hi"');
  });

  it("ignores blank lines, a BOM and trailing whitespace", () => {
    const { rows, problems } = parseSourcesCsv("﻿kind,path\n\n  local , /a \n\n");
    expect(problems).toEqual([]);
    expect(rows).toEqual([{ line: 3, kind: "local", path: "/a" }]);
  });

  // Reported, never dropped: a silently skipped row turns a sync into a
  // deletion nobody asked for.
  it("reports an unknown kind with its line", () => {
    const { rows, problems } = parseSourcesCsv("kind,path\nlocal,/a\nfolder,/b\n");
    expect(rows).toHaveLength(1);
    expect(problems).toEqual([{ line: 3, text: "folder,/b", reason: "kind" }]);
  });

  it("reports a row with no path", () => {
    const { problems } = parseSourcesCsv("local,\n");
    expect(problems[0]).toMatchObject({ line: 1, reason: "empty" });
  });
});

describe("planSync", () => {
  const current = [
    { kind: "local" as const, path: "/keep" },
    { kind: "local" as const, path: "/gone" },
  ];

  it("works out what is added, removed and kept", () => {
    const { rows } = parseSourcesCsv("local,/keep\nlocal,/new\n");
    const plan = planSync(current, rows);
    expect(plan.add.map((s) => s.path)).toEqual(["/new"]);
    expect(plan.remove.map((s) => s.path)).toEqual(["/gone"]);
    expect(plan.keep.map((s) => s.path)).toEqual(["/keep"]);
    expect(plan.next.map((s) => s.path)).toEqual(["/keep", "/new"]);
  });

  // A sync removes. An empty file means an empty list, and the plan has to say
  // so plainly rather than look like a no-op.
  it("treats an empty file as removing everything", () => {
    const plan = planSync(current, []);
    expect(plan.remove).toHaveLength(2);
    expect(plan.next).toEqual([]);
  });

  it("counts the same folder listed twice as once", () => {
    const { rows } = parseSourcesCsv("local,/a\nlocal,/a\n");
    expect(planSync([], rows).next.map((s) => s.path)).toEqual(["/a"]);
  });
});

describe("toSourcesCsv", () => {
  it("round-trips through the parser", () => {
    const refs = [
      { kind: "local" as const, path: "/Users/me/a,b" },
      { kind: "external" as const, path: "https://example.com/a.md" },
    ];
    const { rows, problems } = parseSourcesCsv(toSourcesCsv(refs));
    expect(problems).toEqual([]);
    expect(rows.map((r) => ({ kind: r.kind, path: r.path }))).toEqual(refs);
  });
});
