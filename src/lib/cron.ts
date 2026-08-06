// Cron evaluation for the Daily calendar.
//
// This mirrors the host agent's matcher (hostagent/internal/api/schedules.go,
// `cronMatches`) field for field, and that is the whole requirement: the
// calendar's job is to say when a schedule WILL fire, so it has to agree with
// the code that actually fires it. Two divergences would be easy to introduce
// and both would make the calendar lie:
//
//   - Standard cron treats day-of-month and day-of-week as an OR when both are
//     restricted. The host agent ANDs all five fields. Implementing the
//     standard rule here would draw firings that never happen.
//   - An unsupported token (a range like "1-5") makes the host agent match
//     nothing rather than erroring. Accepting ranges here would draw firings
//     the host agent will silently skip.
//
// Supported per field: "*", a number, a step "*/n", and a comma list of those.
//
// Everything here is pure arithmetic except describeCron, which renders a
// sentence and therefore needs the active language.

import i18n from "@/i18n";

/** Field order of a 5-field expression: minute hour day-of-month month day-of-week. */
const FIELD_COUNT = 5;

/** fieldMatches reports whether one cron field accepts the value v. */
function fieldMatches(field: string, v: number): boolean {
  if (field === "*") return true;
  if (field.includes(",")) return field.split(",").some((part) => fieldMatches(part, v));
  if (field.startsWith("*/")) {
    const n = Number(field.slice(2));
    if (!Number.isInteger(n) || n <= 0) return false;
    return v % n === 0;
  }
  const n = Number(field);
  if (!Number.isInteger(n)) return false; // unsupported token (e.g. "1-5")
  return v === n;
}

function fields(expr: string): string[] | null {
  const f = expr.trim().split(/\s+/).filter(Boolean);
  return f.length === FIELD_COUNT ? f : null;
}

/** Whether the expression fires at this exact minute. */
export function cronMatches(expr: string, d: Date): boolean {
  const f = fields(expr);
  if (!f) return false;
  const vals = [d.getMinutes(), d.getHours(), d.getDate(), d.getMonth() + 1, d.getDay()];
  return f.every((field, i) => fieldMatches(field, vals[i]));
}

/**
 * Whether the expression can fire at all on this calendar day — the date
 * fields only. Cheap enough to filter a month grid with before enumerating
 * times.
 */
export function cronMatchesDay(expr: string, d: Date): boolean {
  const f = fields(expr);
  if (!f) return false;
  return fieldMatches(f[2], d.getDate()) && fieldMatches(f[3], d.getMonth() + 1) && fieldMatches(f[4], d.getDay());
}

/**
 * Every "HH:MM" the expression fires at on the given day, ascending.
 *
 * Capped: a per-minute expression ("* * * * *") would otherwise produce 1440
 * entries per day and 60k for a month grid, which is a calendar nobody can read
 * and a lot of DOM. The cap is reported by `cronTimesCapped` so the UI can say
 * so rather than silently truncating.
 */
export const MAX_TIMES_PER_DAY = 24;

export function cronTimesOnDate(expr: string, d: Date): string[] {
  if (!cronMatchesDay(expr, d)) return [];
  const f = fields(expr);
  if (!f) return [];
  const out: string[] = [];
  for (let h = 0; h < 24; h++) {
    if (!fieldMatches(f[1], h)) continue;
    for (let m = 0; m < 60; m++) {
      if (!fieldMatches(f[0], m)) continue;
      out.push(`${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`);
      if (out.length >= MAX_TIMES_PER_DAY) return out;
    }
  }
  return out;
}

/** Whether cronTimesOnDate had to stop early for this day. */
export function cronTimesCapped(expr: string, d: Date): boolean {
  return cronTimesOnDate(expr, d).length >= MAX_TIMES_PER_DAY;
}

/** A human-readable summary of an expression, for a schedule row. */
export function describeCron(expr: string): string {
  const f = fields(expr);
  if (!f) return expr;
  const [min, hour, dom, mon, dow] = f;
  const at = hour !== "*" && min !== "*" && !hour.includes(",") && !min.includes(",") && !hour.startsWith("*/") && !min.startsWith("*/")
    ? `${String(Number(hour)).padStart(2, "0")}:${String(Number(min)).padStart(2, "0")}`
    : null;
  const days = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"];
  if (at && dom === "*" && mon === "*" && dow === "*") return i18n.t("daily.cron.everyDay", { at });
  if (at && dom === "*" && mon === "*" && /^\d$/.test(dow)) {
    return i18n.t("daily.cron.everyWeek", { day: i18n.t(`daily.weekdaysShort.${days[Number(dow)]}`), at });
  }
  if (at && mon === "*" && dow === "*" && /^\d+$/.test(dom)) return i18n.t("daily.cron.everyMonth", { dom, at });
  return expr;
}

/** The schedule form's view of a cron expression. */
export interface CronForm {
  /** "HH:MM", ascending. */
  times: string[];
  freq: "daily" | "weekly" | "monthly";
  /** 0 = Sunday. Empty for a daily schedule. */
  dows: number[];
  /** 1-12. Empty unless monthly. */
  months: number[];
}

/** Parses a comma list of plain integers, or null if it is anything else. */
function intList(field: string, min: number, max: number): number[] | null {
  const out: number[] = [];
  for (const part of field.split(",")) {
    const n = Number(part.trim());
    if (!Number.isInteger(n) || n < min || n > max) return null;
    out.push(n);
  }
  return [...new Set(out)].sort((a, b) => a - b);
}

/**
 * Turns an expression back into the fields the schedule form edits — the
 * inverse of the form's own cron builder.
 *
 * Returns null for anything the form cannot represent: a step, a range, several
 * distinct minutes, a day-of-month restriction. That distinction is the point.
 * A hand-written every-15-minutes expression opened in the form and saved
 * again would otherwise come back as something quite different, silently
 * rewriting a schedule the user never meant to touch — so the caller keeps
 * the original expression instead of pretending it round-tripped.
 */
export function parseCron(expr: string): CronForm | null {
  const f = fields(expr);
  if (!f) return null;
  const [minute, hour, dom, mon, dow] = f;

  // The form always leaves day-of-month open; a restriction here means the
  // expression came from somewhere else.
  if (dom !== "*") return null;

  const minutes = intList(minute, 0, 59);
  if (!minutes || minutes.length !== 1) return null;
  const hours = intList(hour, 0, 23);
  if (!hours) return null;

  const months = mon === "*" ? [] : intList(mon, 1, 12);
  if (months === null) return null;
  const dows = dow === "*" ? [] : intList(dow, 0, 6);
  if (dows === null) return null;

  const mm = String(minutes[0]).padStart(2, "0");
  return {
    times: hours.map((h) => `${String(h).padStart(2, "0")}:${mm}`),
    freq: months.length > 0 ? "monthly" : dows.length > 0 ? "weekly" : "daily",
    dows,
    months,
  };
}
