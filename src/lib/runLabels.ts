import { daily } from "./daily";
import { runs } from "./runs";

// Naming a run the way a person would.
//
// Everything downstream of a launch identifies a run by the orchestrator's id —
// the gateway logs it, the trace is keyed by it, the audit list shows it. That
// is the right identity for machinery and the wrong one for a screen: nobody
// remembers which piece of work `run-4f2a…` was, and the whole point of reading
// a knowledge trace is to decide something about a particular task.
//
// The name exists, just not where the id does. Delivery records the task it
// launched, schedules record the occurrence they fired, and both keep the run
// id alongside. So this joins the two histories into one lookup and leaves the
// id in place rather than replacing it: the id is still what you paste into a
// log search, and the title is what tells you whether you want to.

export interface RunLabel {
  /** what a person calls this work — the task title, or the schedule's name. */
  title: string;
  /** where it came from: repo · branch for Delivery, the template for Daily. */
  sub: string;
  kind: "Delivery" | "Daily";
}

/** How far back either history is read. A trace older than this is still
 *  perfectly viewable; it simply shows its id, which is what it did before. */
const LOOKBACK = 200;

/**
 * fetchRunLabels builds runId → label from both run histories.
 *
 * Settled rather than all: the two are separate host routes and one being
 * unavailable should cost its own runs their names, not everyone's. A total
 * failure returns an empty map, which every caller already handles — it is the
 * same state as a run nobody recorded.
 */
export async function fetchRunLabels(): Promise<Map<string, RunLabel>> {
  const [scheduled, manual] = await Promise.allSettled([daily.runs(LOOKBACK), runs.list(LOOKBACK)]);
  const out = new Map<string, RunLabel>();

  if (scheduled.status === "fulfilled") {
    for (const r of scheduled.value) {
      if (!r.runId) continue; // an occurrence that launched nothing
      out.set(r.runId, {
        title: r.name || r.scheduleId,
        sub: r.template || r.perspective || "",
        kind: "Daily",
      });
    }
  }
  // Delivery second, so it wins a collision. The two histories should never
  // name the same run, and if they somehow do, the task title is the more
  // specific answer of the two.
  if (manual.status === "fulfilled") {
    for (const r of manual.value) {
      if (!r.runId) continue;
      out.set(r.runId, {
        title: r.task || r.name || r.runId,
        sub: [r.repo, r.branch].filter(Boolean).join(" · "),
        kind: "Delivery",
      });
    }
  }
  return out;
}
