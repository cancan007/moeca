import type { KnowledgeSource } from "./knowledgeSources";

// Reading and writing the reference list as CSV.
//
// The list is short and the thing people want to do with it is bulk: paste the
// folders a new machine should index, or lift the set from one install into
// another. Clicking a native folder picker once per row is not that.
//
// Parsing is deliberately forgiving about shape and strict about content. A
// spreadsheet will add a header, an editor will add a BOM, and a hand-written
// file will have a trailing newline — none of those are the user's mistake, so
// none of them are errors. A row naming something that is not a folder IS an
// error, and it is reported with its line number rather than dropped, because a
// silently skipped row turns a sync into a deletion nobody asked for.

/** One row that parsed, or one that did not. Both carry the source line so the
 *  screen can point at it. */
export interface CsvRow {
  line: number;
  kind: "local" | "external";
  path: string;
}

export interface CsvProblem {
  line: number;
  text: string;
  reason: "kind" | "empty";
}

export interface ParsedCsv {
  rows: CsvRow[];
  problems: CsvProblem[];
}

/** splitCsvLine splits one line on commas, honouring quotes.
 *
 *  A path can contain a comma, so the naive split is wrong for exactly the case
 *  this feature exists to serve. Doubled quotes inside a quoted field are one
 *  quote, which is what every spreadsheet writes. */
function splitCsvLine(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quoted = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (quoted) {
      if (c === '"') {
        if (line[i + 1] === '"') {
          cur += '"';
          i++;
        } else {
          quoted = false;
        }
      } else {
        cur += c;
      }
      continue;
    }
    if (c === '"') quoted = true;
    else if (c === ",") {
      out.push(cur);
      cur = "";
    } else cur += c;
  }
  out.push(cur);
  return out.map((f) => f.trim());
}

/** looksLikeHeader reports whether the first row names columns rather than a
 *  reference. Checked by content, not by position: a file that starts straight
 *  into data must not lose its first row. */
function looksLikeHeader(fields: string[]): boolean {
  const first = (fields[0] ?? "").toLowerCase();
  return first === "kind" || first === "type" || first === "種別";
}

/**
 * parseSourcesCsv reads `kind,path` rows.
 *
 * One column is accepted too, and the kind is inferred: an https URL is an
 * external document and anything else is a folder. That is the whole rule the
 * two kinds differ by, so making someone write it out adds a column of noise
 * and a column of typos.
 */
export function parseSourcesCsv(text: string): ParsedCsv {
  const rows: CsvRow[] = [];
  const problems: CsvProblem[] = [];
  // Strip a BOM; editors add one and it would otherwise become part of the
  // first field, turning "kind" into something that is not "kind".
  const lines = text.replace(/^﻿/, "").split(/\r?\n/);

  lines.forEach((raw, i) => {
    const line = i + 1;
    if (!raw.trim()) return; // blank lines separate, they do not mean anything
    const fields = splitCsvLine(raw);
    if (i === 0 && looksLikeHeader(fields)) return;

    let kind = "";
    let path = "";
    if (fields.length === 1) {
      path = fields[0];
      kind = path.toLowerCase().startsWith("https://") ? "external" : "local";
    } else {
      kind = (fields[0] ?? "").toLowerCase();
      path = fields.slice(1).join(",").trim();
      // Tolerated the other way round as well: a two-column file whose first
      // column is the path reads unambiguously, and refusing it would be
      // insisting on an order the file did not know about.
      if (kind !== "local" && kind !== "external" && !path) {
        path = fields[0];
        kind = path.toLowerCase().startsWith("https://") ? "external" : "local";
      }
    }

    if (!path) {
      problems.push({ line, text: raw.trim(), reason: "empty" });
      return;
    }
    if (kind !== "local" && kind !== "external") {
      problems.push({ line, text: raw.trim(), reason: "kind" });
      return;
    }
    rows.push({ line, kind, path });
  });

  return { rows, problems };
}

/** The change a parsed file would make, worked out before anything is written.
 *
 *  A sync removes, so what leaves has to be shown before it goes. "kept" is
 *  there for the same reason: a diff that only lists changes cannot be told
 *  apart from one that misread the whole file. */
export interface CsvPlan {
  add: KnowledgeSource[];
  remove: KnowledgeSource[];
  keep: KnowledgeSource[];
  /** the list as it would be after applying, in the file's order. */
  next: KnowledgeSource[];
}

/** planSync diffs a parsed file against what is registered now.
 *
 *  Identity is the path. The kind is derived from it in practice, and a row
 *  that changed only its kind would be the same reference described two ways
 *  rather than a different one. */
export function planSync(current: KnowledgeSource[], rows: CsvRow[]): CsvPlan {
  const seen = new Set<string>();
  const next: KnowledgeSource[] = [];
  for (const r of rows) {
    if (seen.has(r.path)) continue; // the same folder twice is once
    seen.add(r.path);
    next.push({ kind: r.kind, path: r.path });
  }
  const now = new Map(current.map((c) => [c.path, c]));
  return {
    add: next.filter((n) => !now.has(n.path)),
    remove: current.filter((c) => !seen.has(c.path)),
    keep: next.filter((n) => now.has(n.path)),
    next,
  };
}

/** toSourcesCsv writes the current list back out.
 *
 *  Exists so the round trip works: export, edit in a spreadsheet, import. It
 *  also makes the format self-documenting, which is why the header is written
 *  even though reading one is optional. */
export function toSourcesCsv(refs: KnowledgeSource[]): string {
  const field = (v: string) => (/[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);
  return ["kind,path", ...refs.map((r) => `${r.kind},${field(r.path)}`)].join("\n") + "\n";
}
