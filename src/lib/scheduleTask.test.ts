import { describe, it, expect } from "vitest";
import { scheduleTask } from "./schedules";

// The form collects a name, a goal and milestones; only the goal used to reach
// the agents. A schedule called 犬の画像作成 whose goal was 画像作成 handed its
// agents four characters and no mention of a dog.

describe("the task a schedule hands its agents", () => {
  it("carries the milestones, not just the goal", () => {
    const task = scheduleTask({
      name: "犬の画像作成",
      goal: "画像作成",
      milestones: [{ title: "可愛い芝犬をモデルにする" }, { title: "画像として出力" }],
    });
    expect(task).toContain("可愛い芝犬をモデルにする");
    expect(task).toContain("画像として出力");
    // Numbered, because they are acceptance criteria rather than prose.
    expect(task).toContain("1.");
    expect(task).toContain("2.");
  });

  // The name is the only place the subject appears in that example, so it has
  // to survive alongside the goal.
  it("keeps a name that says more than the goal", () => {
    const task = scheduleTask({ name: "犬の画像作成", goal: "画像作成", milestones: [{ title: "x" }] });
    expect(task).toContain("犬の画像作成");
    expect(task).toContain("画像作成");
  });

  it("does not repeat a name identical to the goal", () => {
    const task = scheduleTask({ name: "同じ", goal: "同じ", milestones: [{ title: "x" }] });
    expect(task.split("同じ").length - 1).toBe(1);
  });

  // Unchanged for a schedule with no milestones: it is still just the goal.
  it("is the goal alone when there are no milestones", () => {
    expect(scheduleTask({ name: "n", goal: "g" })).toBe("g");
    expect(scheduleTask({ name: "n", goal: "g", milestones: [] })).toBe("g");
  });

  it("falls back to the name when there is no goal", () => {
    expect(scheduleTask({ name: "n" })).toBe("n");
  });

  it("ignores blank milestone rows the form leaves behind", () => {
    const task = scheduleTask({ goal: "g", milestones: [{ title: "  " }] });
    expect(task).toBe("g");
  });
});
