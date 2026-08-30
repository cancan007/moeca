import type { AccessLog } from "@/lib/gateway";

// Reconstructing what a run reached in the knowledge base.
//
// Every retrieval an agent performs goes through the gateway, which records the
// request and the response. That is enough to say which sources came back to
// which stage — and it is *only* enough to say that.
//
// Two shapes count as a retrieval, because there are two ways to reach a
// source. A search answers with chunks and names where each came from; a fetch
// follows one of those names and returns the document or the file itself. The
// second is how a picture is reached at all — an image is indexed as metadata,
// so no search will ever return its contents — and a trace that only understood
// searches would show the reference image as never touched by a run that opened
// it. Both are read here, from whichever end of the exchange carries the name.
//
// What this cannot say is what the model actually read. A stage that was handed
// five chunks and used one leaves the same trace as a stage that used all five,
// so every count here is an upper bound. The UI says "reached", not "read"
// for that reason, and the distinction matters when the screen is being used to
// decide a group is unnecessary: an unreached group is provably unused, while a
// reached one is only possibly useful.

/** One retrieval, as the gateway saw it. */
export interface TraceQuery {
  requestId: string;
  time: string;
  query: string;
  sources: string[];
  /** the recorded response was cut short, so `sources` is incomplete. */
  truncated: boolean;
}

export interface TraceStage {
  id: string;
  queries: TraceQuery[];
  /** how many times each source came back to this stage. */
  reached: Map<string, number>;
  truncated: boolean;
}

export interface Trace {
  run: string;
  stages: TraceStage[];
  /** union across stages, with per-source totals. */
  reached: Map<string, number>;
  queryCount: number;
  /** any stage lost part of its record. */
  truncated: boolean;
}

export const EMPTY_TRACE: Trace = {
  run: "",
  stages: [],
  reached: new Map(),
  queryCount: 0,
  truncated: false,
};

/** isRetrieval picks the knowledge searches out of the log.
 *
 *  Matched on the service the gateway routed to rather than the path, since the
 *  prefix is configurable and the service name is what the gateway itself used
 *  to make the authorization decision. */
function isRetrieval(l: AccessLog): boolean {
  return l.service === "rag" && l.status >= 200 && l.status < 300;
}

/** isSourceFetch distinguishes following a search result from making one.
 *
 *  Matched on the path's tail rather than the whole path, since the gateway
 *  prefix a run reaches the indexer under is configurable. */
function isSourceFetch(l: AccessLog): boolean {
  return l.path.replace(/[?#].*$/, "").endsWith("/source");
}

/** parseSources pulls the source names out of a recorded search response.
 *
 *  A capture that was cut short is reported rather than silently short: the
 *  gateway caps how much of a body it keeps, and a long response loses its
 *  tail. Reading a truncated list as complete would understate what a stage
 *  reached, which is the direction that produces a wrong "this group was never
 *  used" conclusion. */
function parseSources(l: AccessLog): { sources: string[]; truncated: boolean } {
  // A fetch names its source in the REQUEST, and that is the better place to
  // read it from: the response is the document, which for a picture is bytes
  // that no JSON parse will survive. Reading the request also means a fetch
  // that returned a 200 counts as reached whichever form it asked for.
  if (isSourceFetch(l)) {
    try {
      const asked = (JSON.parse(l.reqBody ?? "") as { source?: string }).source;
      return { sources: typeof asked === "string" && asked ? [asked] : [], truncated: false };
    } catch {
      // The request body is small and the gateway keeps it whole, so failing
      // here means it was not captured at all rather than cut short.
      return { sources: [], truncated: l.reqBytes > 0 };
    }
  }
  const body = l.respBody ?? "";
  if (!body) {
    // Nothing captured at all. If bytes crossed the wire, content capture is
    // off or the body was dropped; either way the sources are unknown.
    return { sources: [], truncated: l.respBytes > 0 };
  }
  // respBytes counts what the upstream actually sent; a shorter capture means
  // the record is partial even when it happens to still parse.
  const short = l.respBytes > 0 && body.length < l.respBytes;
  try {
    const parsed = JSON.parse(body) as { results?: { source?: string }[] };
    const sources = (parsed.results ?? [])
      .map((r) => r.source)
      .filter((s): s is string => typeof s === "string" && s.length > 0);
    return { sources, truncated: short };
  } catch {
    // Truncated JSON is the common case here, and the salvage is worth doing:
    // the sources that survived are real, and reporting none of them would
    // look exactly like a stage that retrieved nothing.
    const found = [...body.matchAll(/"source"\s*:\s*"((?:[^"\\]|\\.)*)"/g)].map((m) =>
      m[1].replace(/\\(.)/g, "$1"),
    );
    return { sources: found, truncated: true };
  }
}

function parseQuery(l: AccessLog): string {
  try {
    // A search carries a query; a fetch carries the source it is following, and
    // recording that keeps the two legible as one sequence — searched for this,
    // then went and read that.
    const parsed = JSON.parse(l.reqBody ?? "") as { query?: string; source?: string };
    if (typeof parsed.query === "string") return parsed.query;
    if (typeof parsed.source === "string") return parsed.source;
    return "";
  } catch {
    return "";
  }
}

/** buildTrace groups a run's retrievals by stage, oldest first.
 *
 *  Ordering is by timestamp rather than by position in the log, because the
 *  gateway serves its ring buffer newest-first — taking the log's order would
 *  present the run backwards, which on a screen whose whole point is "how did
 *  this progress" is worse than presenting nothing.
 *
 *  Stages that never retrieved anything do not appear here at all; the caller
 *  pairs this against the run's declared stage list to show those. */
export function buildTrace(logs: AccessLog[], run: string): Trace {
  if (!run) return EMPTY_TRACE;
  const stages: TraceStage[] = [];
  const byId = new Map<string, TraceStage>();
  const reached = new Map<string, number>();
  let queryCount = 0;
  let truncated = false;

  // Sorted oldest-first before grouping, so both the stage order and each
  // stage's query list read forwards.
  //
  // The tie-break is reversed on purpose. Timestamps are second-granular and a
  // stage can issue several searches within one, so equal stamps are common;
  // the gateway serves its ring buffer newest-first, which makes a later log
  // position the *earlier* request.
  const ordered = logs
    .filter((l) => l.run === run && isRetrieval(l))
    .map((l, i) => ({ l, i }))
    .sort((a, b) => a.l.time.localeCompare(b.l.time) || b.i - a.i)
    .map(({ l }) => l);

  for (const l of ordered) {
    // A retrieval with no stage attribution still belongs to the run; bucket it
    // rather than dropping it, or the totals would disagree with the stages.
    const id = l.stage || "(unattributed)";
    let stage = byId.get(id);
    if (!stage) {
      stage = { id, queries: [], reached: new Map(), truncated: false };
      byId.set(id, stage);
      stages.push(stage);
    }
    const { sources, truncated: cut } = parseSources(l);
    stage.queries.push({
      requestId: l.requestId,
      time: l.time,
      query: parseQuery(l),
      sources,
      truncated: cut,
    });
    queryCount++;
    if (cut) {
      stage.truncated = true;
      truncated = true;
    }
    for (const s of sources) {
      stage.reached.set(s, (stage.reached.get(s) ?? 0) + 1);
      reached.set(s, (reached.get(s) ?? 0) + 1);
    }
  }
  return { run, stages, reached, queryCount, truncated };
}

/** runIds lists the runs present in the log, most recent first, so the screen
 *  can offer a trace without being handed one. */
export function runIds(logs: AccessLog[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (let i = logs.length - 1; i >= 0; i--) {
    const r = logs[i].run;
    if (r && !seen.has(r)) {
      seen.add(r);
      out.push(r);
    }
  }
  return out;
}
