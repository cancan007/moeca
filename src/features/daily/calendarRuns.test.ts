import { describe, it, expect } from "vitest";
import { calendarRuns } from "./calendarRuns";
import type { Schedule } from "@/lib/schedules";
import type { ScheduleRun } from "@/lib/daily";

const TODAY = new Date(2026, 7, 4, 12, 0); // Tue 2026-08-04, midday
const YESTERDAY = new Date(2026, 7, 3, 12, 0);
const TOMORROW = new Date(2026, 7, 5, 12, 0);

const schedule = (over: Partial<Schedule> = {}): Schedule =>
  ({
    id: "sch-1", name: "競合UI監視", cron: "0 8 * * *", perspective: "discovery",
    task: "", active: true, lastRun: "", runCount: 0, goal: "", milestones: [],
    templateLabel: "", templateRef: "",
    ...over,
  }) as Schedule;

/** An occurrence at a local wall-clock time on the given day. */
const occurrence = (d: Date, h: number, m: number, over: Partial<ScheduleRun> = {}): ScheduleRun =>
  ({
    id: 1, scheduleId: "sch-1", name: "競合UI監視", perspective: "discovery",
    scheduledAt: new Date(d.getFullYear(), d.getMonth(), d.getDate(), h, m).toISOString(),
    status: "executed", outputDir: "/tmp/out", containerId: "", runId: "run-1", template: "",
    ...over,
  }) as ScheduleRun;

describe("looking back", () => {
  // A past day drawn from cron would rewrite history every time a schedule is
  // edited, paused or deleted.
  it("shows what actually happened, not what the cron says", () => {
    const s = [schedule({ cron: "0 8 * * *" })];
    const occ = [occurrence(YESTERDAY, 8, 0, { status: "failed" })];

    const runs = calendarRuns(YESTERDAY, s, occ, TODAY);
    expect(runs).toHaveLength(1);
    expect(runs[0].status).toBe("failed");
  });

  it("draws nothing for a past day that never fired", () => {
    const s = [schedule({ cron: "0 8 * * *" })];
    expect(calendarRuns(YESTERDAY, s, [], TODAY)).toEqual([]);
  });

  it("keeps an occurrence whose schedule was since deleted", () => {
    const runs = calendarRuns(YESTERDAY, [], [occurrence(YESTERDAY, 8, 0)], TODAY);
    expect(runs).toHaveLength(1);
    expect(runs[0].name).toBe("競合UI監視");
    expect(runs[0].active).toBe(false);
  });

  it("surfaces a run missed while the app was down", () => {
    const runs = calendarRuns(YESTERDAY, [schedule()], [occurrence(YESTERDAY, 8, 0, { status: "missed" })], TODAY);
    expect(runs[0].status).toBe("missed");
  });
});

describe("looking forward", () => {
  it("projects the active schedules' firings", () => {
    const s = [schedule({ cron: "0 8,18 * * *" })];
    const runs = calendarRuns(TOMORROW, s, [], TODAY);
    expect(runs.map((r) => r.time)).toEqual(["08:00", "18:00"]);
    expect(runs[0].status).toBeUndefined(); // planned, not a result
  });

  it("leaves a paused schedule off the calendar", () => {
    const s = [schedule({ active: false })];
    expect(calendarRuns(TOMORROW, s, [], TODAY)).toEqual([]);
  });

  it("respects the cron's day fields", () => {
    const s = [schedule({ cron: "0 7 * * 1" })]; // Mondays
    expect(calendarRuns(TOMORROW, s, [], TODAY)).toEqual([]); // Wednesday
    expect(calendarRuns(new Date(2026, 7, 10, 12, 0), s, [], TODAY)).toHaveLength(1); // Monday
  });
});

describe("today is both", () => {
  it("shows what already fired alongside what is still to come", () => {
    const s = [schedule({ cron: "0 8,18 * * *" })];
    const occ = [occurrence(TODAY, 8, 0)];

    const runs = calendarRuns(TODAY, s, occ, TODAY);
    expect(runs.map((r) => [r.time, r.status])).toEqual([
      ["08:00", "executed"],
      ["18:00", undefined],
    ]);
  });

  // The moment a schedule fires, its entry must turn from a plan into a
  // result rather than appearing twice.
  it("does not double-count a firing that already has an occurrence", () => {
    const s = [schedule({ cron: "0 8 * * *" })];
    const runs = calendarRuns(TODAY, s, [occurrence(TODAY, 8, 0)], TODAY);
    expect(runs).toHaveLength(1);
    expect(runs[0].status).toBe("executed");
    expect(runs[0].run?.runId).toBe("run-1");
  });

  it("keeps two schedules that happen to fire at the same minute", () => {
    const s = [schedule({ id: "a", name: "A" }), schedule({ id: "b", name: "B" })];
    const runs = calendarRuns(TOMORROW, s, [], TODAY);
    expect(runs.map((r) => r.name)).toEqual(["A", "B"]);
  });
});

describe("ordering and attribution", () => {
  it("sorts by time across both sources", () => {
    const s = [schedule({ id: "a", name: "朝", cron: "0 6 * * *" }), schedule({ id: "b", name: "夜", cron: "0 22 * * *" })];
    const occ = [occurrence(TODAY, 12, 30, { scheduleId: "c", name: "昼" })];
    const runs = calendarRuns(TODAY, s, occ, TODAY);
    expect(runs.map((r) => r.name)).toEqual(["朝", "昼", "夜"]);
  });

  it("carries the occurrence through so the artifacts stay reachable", () => {
    const runs = calendarRuns(TODAY, [schedule()], [occurrence(TODAY, 8, 0)], TODAY);
    expect(runs[0].run?.outputDir).toBe("/tmp/out");
  });
});
