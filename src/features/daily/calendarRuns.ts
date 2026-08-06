import type { Schedule } from "@/lib/schedules";
import type { ScheduleRun } from "@/lib/daily";
import { cronTimesOnDate } from "@/lib/cron";
import i18n from "@/i18n";

// What the Daily calendar shows for a given day.
//
// A calendar of scheduled work has to answer two different questions depending
// on which way you look from today, and conflating them is what makes one
// useless:
//
//   - Looking back, the only honest answer is what ACTUALLY happened. A past
//     day is drawn from recorded occurrences, so a schedule that was paused,
//     edited, or whose cron changed does not retroactively rewrite history —
//     and a run that was missed while the app was down shows as missed rather
//     than silently as "it ran".
//   - Looking forward, there are no occurrences yet, so a future day is
//     projected from the active schedules' cron expressions.
//
// Today is the only day that is both: what has already fired, plus what is
// still to come.

export interface CalendarRun {
  /** "HH:MM", local time. */
  time: string;
  name: string;
  /** discovery | context-opt | automation — drives the colour. */
  perspective: string;
  scheduleId: string;
  /** Absent for a projected (not yet fired) run. */
  status?: ScheduleRun["status"];
  /** The occurrence, when this run actually happened — carries the artifacts. */
  run?: ScheduleRun;
  /** Whether the schedule behind this run is currently enabled. */
  active: boolean;
}

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

function hhmm(d: Date): string {
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** Midnight of d, for comparing whole days without time-of-day noise. */
function startOfDay(d: Date): number {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
}

/**
 * The runs to draw on `date`: recorded occurrences, plus — for today and the
 * future — the firings the active schedules still project.
 *
 * A projected run is dropped when an occurrence already covers the same
 * schedule at the same minute, so the moment a schedule fires its calendar
 * entry turns from a plan into a result rather than appearing twice.
 */
export function calendarRuns(
  date: Date,
  schedules: Schedule[],
  occurrences: ScheduleRun[],
  today: Date,
): CalendarRun[] {
  const byId = new Map(schedules.map((s) => [s.id, s]));
  const out: CalendarRun[] = [];
  const taken = new Set<string>();

  for (const occ of occurrences) {
    const at = new Date(occ.scheduledAt);
    if (Number.isNaN(at.getTime()) || !sameDay(at, date)) continue;
    const time = hhmm(at);
    taken.add(`${occ.scheduleId}@${time}`);
    out.push({
      time,
      name: occ.name,
      perspective: occ.perspective || byId.get(occ.scheduleId)?.perspective || "automation",
      scheduleId: occ.scheduleId,
      status: occ.status,
      run: occ,
      active: byId.get(occ.scheduleId)?.active ?? false,
    });
  }

  // Projecting into the past would invent runs that never happened — the
  // schedule may not have existed, or its cron may since have changed.
  if (startOfDay(date) >= startOfDay(today)) {
    for (const s of schedules) {
      if (!s.active) continue;
      for (const time of cronTimesOnDate(s.cron, date)) {
        if (taken.has(`${s.id}@${time}`)) continue;
        out.push({
          time,
          name: s.name,
          perspective: s.perspective || "automation",
          scheduleId: s.id,
          active: true,
        });
      }
    }
  }

  return out.sort((a, b) => (a.time === b.time ? a.name.localeCompare(b.name) : a.time.localeCompare(b.time)));
}

/** Label + colour bucket for a run's state, for the calendar and side panel. */
export function runStateLabel(r: CalendarRun): { label: string; color: string } {
  switch (r.status) {
    case "executed":
      return { label: i18n.t("daily.runState.executed"), color: "#67c9a4" };
    case "failed":
      return { label: i18n.t("daily.runState.failed"), color: "#e06a6a" };
    case "missed":
      return { label: i18n.t("daily.runState.missed"), color: "#d39a4e" };
    default:
      return { label: i18n.t("daily.runState.scheduled"), color: "var(--tx-dim)" };
  }
}
