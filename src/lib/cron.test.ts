import { describe, it, expect, beforeAll } from "vitest";
import { cronMatches, cronMatchesDay, cronTimesOnDate, describeCron, parseCron, MAX_TIMES_PER_DAY } from "./cron";
import i18n from "@/i18n";

// 2026-08-04 is a Tuesday (getDay() === 2).
const tue = (h = 0, m = 0) => new Date(2026, 7, 4, h, m);
const mon = (h = 0, m = 0) => new Date(2026, 7, 3, h, m);

describe("cronMatches", () => {
  it("matches a daily expression at its minute only", () => {
    expect(cronMatches("0 8 * * *", tue(8, 0))).toBe(true);
    expect(cronMatches("0 8 * * *", tue(8, 1))).toBe(false);
    expect(cronMatches("0 8 * * *", tue(9, 0))).toBe(false);
  });

  it("supports steps and comma lists", () => {
    expect(cronMatches("*/15 * * * *", tue(3, 30))).toBe(true);
    expect(cronMatches("*/15 * * * *", tue(3, 31))).toBe(false);
    expect(cronMatches("0 8,18 * * *", tue(18, 0))).toBe(true);
    expect(cronMatches("0 8,18 * * *", tue(12, 0))).toBe(false);
  });

  it("rejects an expression that is not five fields", () => {
    expect(cronMatches("0 8 * *", tue(8, 0))).toBe(false);
    expect(cronMatches("", tue(8, 0))).toBe(false);
    expect(cronMatches("0 8 * * * *", tue(8, 0))).toBe(false);
  });
});

// The calendar exists to say when a schedule WILL fire, so it has to agree with
// the host agent that fires it. These two are where a "more correct"
// implementation would quietly start lying.
describe("agreement with the host agent's matcher", () => {
  it("ANDs day-of-month with day-of-week (not the standard OR)", () => {
    // Standard cron fires on the 3rd OR any Tuesday. The host agent requires
    // both, so the 3rd (a Monday) must not match.
    expect(cronMatches("0 8 3 * 2", mon(8, 0))).toBe(false);
    expect(cronMatches("0 8 4 * 2", tue(8, 0))).toBe(true);
  });

  it("treats a range as unsupported rather than expanding it", () => {
    // "1-5" is a weekday range in standard cron; the host agent matches
    // nothing, so drawing it would show firings that never happen.
    expect(cronMatches("30 9 * * 1-5", tue(9, 30))).toBe(false);
    expect(cronTimesOnDate("30 9 * * 1-5", tue())).toEqual([]);
  });
});

describe("cronMatchesDay", () => {
  it("ignores the time fields", () => {
    expect(cronMatchesDay("0 8 * * *", tue())).toBe(true);
    expect(cronMatchesDay("0 8 * * 1", tue())).toBe(false);
    expect(cronMatchesDay("0 8 * * 1", mon())).toBe(true);
  });
});

describe("cronTimesOnDate", () => {
  it("lists the firing times of the day in order", () => {
    expect(cronTimesOnDate("0 8 * * *", tue())).toEqual(["08:00"]);
    expect(cronTimesOnDate("0 8,18 * * *", tue())).toEqual(["08:00", "18:00"]);
    expect(cronTimesOnDate("0,30 9 * * *", tue())).toEqual(["09:00", "09:30"]);
  });

  it("returns nothing on a day the expression does not cover", () => {
    expect(cronTimesOnDate("0 7 * * 1", tue())).toEqual([]);
    expect(cronTimesOnDate("0 7 * * 1", mon())).toEqual(["07:00"]);
  });

  // A per-minute schedule would otherwise put 1440 rows in one calendar cell.
  it("caps a runaway expression instead of rendering every minute", () => {
    const times = cronTimesOnDate("* * * * *", tue());
    expect(times).toHaveLength(MAX_TIMES_PER_DAY);
    expect(times[0]).toBe("00:00");
  });
});

// describeCron renders a sentence, so it depends on the active language. The
// language is pinned here rather than left to detection: what these cases check
// is which SHAPE each expression maps to, and that has to hold in any locale.
describe("describeCron", () => {
  beforeAll(async () => {
    await i18n.changeLanguage("ja");
  });

  it("summarises the common shapes and falls back to the expression", () => {
    expect(describeCron("0 8 * * *")).toBe("毎日 08:00");
    expect(describeCron("30 9 * * 1")).toBe("毎週月 09:30");
    expect(describeCron("0 10 1 * *")).toBe("毎月1日 10:00");
    expect(describeCron("*/15 * * * *")).toBe("*/15 * * * *");
    expect(describeCron("nonsense")).toBe("nonsense");
  });

  it("follows the active language", async () => {
    await i18n.changeLanguage("en");
    expect(describeCron("0 8 * * *")).toBe("Daily at 08:00");
    await i18n.changeLanguage("zh");
    expect(describeCron("0 8 * * *")).toBe("每天 08:00");
    await i18n.changeLanguage("ja");
  });
});

describe("parseCron", () => {
  it("round-trips the shapes the form builds", () => {
    expect(parseCron("0 8 * * *")).toEqual({ times: ["08:00"], freq: "daily", dows: [], months: [] });
    expect(parseCron("30 9 * * 1,3")).toEqual({ times: ["09:30"], freq: "weekly", dows: [1, 3], months: [] });
    expect(parseCron("0 8,18 * * *")).toEqual({ times: ["08:00", "18:00"], freq: "daily", dows: [], months: [] });
    expect(parseCron("0 10 * 1,7 5")).toEqual({ times: ["10:00"], freq: "monthly", dows: [5], months: [1, 7] });
  });

  it("sorts and de-duplicates so the form shows one canonical order", () => {
    expect(parseCron("0 18,8,8 * * *")?.times).toEqual(["08:00", "18:00"]);
    expect(parseCron("0 8 * * 3,1,3")?.dows).toEqual([1, 3]);
  });

  // Opening an expression the form cannot represent and saving it again would
  // silently rewrite a schedule the user never meant to touch. Refusing to
  // parse is what lets the caller keep the original.
  it("refuses anything the form cannot represent", () => {
    expect(parseCron("*/15 * * * *")).toBeNull();      // step
    expect(parseCron("0 9 * * 1-5")).toBeNull();       // range
    expect(parseCron("0,30 8 * * *")).toBeNull();      // two distinct minutes
    expect(parseCron("0 8 15 * *")).toBeNull();        // day-of-month restricted
    expect(parseCron("0 25 * * *")).toBeNull();        // hour out of range
    expect(parseCron("0 8 * * 9")).toBeNull();         // weekday out of range
    expect(parseCron("not a cron")).toBeNull();
    expect(parseCron("")).toBeNull();
  });

  // Whatever the form produces must be readable back by the form.
  it("agrees with what the calendar would fire", () => {
    const form = parseCron("0 8,18 * * *")!;
    expect(cronTimesOnDate("0 8,18 * * *", tue())).toEqual(form.times);
  });
});
