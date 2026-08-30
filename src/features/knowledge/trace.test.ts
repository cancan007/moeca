import { describe, expect, it } from "vitest";
import type { AccessLog } from "@/lib/gateway";
import { buildTrace, passagesFrom, runIds } from "./trace";

function log(p: Partial<AccessLog>): AccessLog {
  return {
    time: "2026-08-01T00:00:00Z",
    requestId: "r1",
    session: "s",
    service: "rag",
    method: "POST",
    path: "/rag/search",
    status: 200,
    reqBytes: 0,
    respBytes: 0,
    durationMs: 3,
    ...p,
  };
}

function results(sources: string[]): string {
  return JSON.stringify({ results: sources.map((s) => ({ source: s, text: "…", score: 0.8 })) });
}

describe("buildTrace", () => {
  it("groups retrievals by stage in the order they first appear", () => {
    const t = buildTrace(
      [
        log({ run: "run-1", stage: "plan", respBody: results(["a.md"]) }),
        log({ run: "run-1", stage: "build", respBody: results(["b.md"]) }),
        log({ run: "run-1", stage: "plan", respBody: results(["a.md", "c.md"]) }),
      ],
      "run-1",
    );
    expect(t.stages.map((s) => s.id)).toEqual(["plan", "build"]);
    expect(t.stages[0].queries).toHaveLength(2);
    // a.md came back twice to this stage.
    expect(t.stages[0].reached.get("a.md")).toBe(2);
    expect(t.reached.get("a.md")).toBe(2);
    expect(t.queryCount).toBe(3);
  });

  it("ignores other runs and non-retrieval traffic", () => {
    const t = buildTrace(
      [
        log({ run: "run-2", stage: "plan", respBody: results(["other.md"]) }),
        log({ run: "run-1", service: "anthropic", path: "/anthropic/v1/messages", respBody: "{}" }),
        log({ run: "run-1", stage: "plan", respBody: results(["a.md"]) }),
      ],
      "run-1",
    );
    expect([...t.reached.keys()]).toEqual(["a.md"]);
  });

  // A failed search returned nothing, so counting it as a reach would invent
  // knowledge the stage never got.
  it("ignores failed retrievals", () => {
    const t = buildTrace(
      [log({ run: "run-1", stage: "plan", status: 403, respBody: results(["denied.md"]) })],
      "run-1",
    );
    expect(t.reached.size).toBe(0);
  });

  // The whole screen exists to decide what a task did not need, so a partial
  // record must never read as a complete one.
  it("flags a capture shorter than the real response", () => {
    const body = results(["a.md"]);
    const t = buildTrace(
      [log({ run: "run-1", stage: "plan", respBody: body, respBytes: body.length * 4 })],
      "run-1",
    );
    expect(t.truncated).toBe(true);
    expect(t.stages[0].truncated).toBe(true);
  });

  it("does not flag a complete capture", () => {
    const body = results(["a.md"]);
    const t = buildTrace(
      [log({ run: "run-1", stage: "plan", respBody: body, respBytes: body.length })],
      "run-1",
    );
    expect(t.truncated).toBe(false);
  });

  // Truncated JSON is the usual shape of a cut capture. Salvaging what parsed
  // beats reporting nothing, which would look like a stage that retrieved
  // nothing at all.
  it("salvages sources from a body cut mid-JSON", () => {
    const cut = `{"results":[{"source":"a.md","text":"xx"},{"source":"b.md","text":"yy`;
    const t = buildTrace(
      [log({ run: "run-1", stage: "plan", respBody: cut, respBytes: 9999 })],
      "run-1",
    );
    expect([...t.reached.keys()]).toEqual(["a.md", "b.md"]);
    expect(t.truncated).toBe(true);
  });

  // Content capture can be off entirely. That is unknown, not empty.
  it("flags a response whose body was not captured at all", () => {
    const t = buildTrace([log({ run: "run-1", stage: "plan", respBytes: 500 })], "run-1");
    expect(t.truncated).toBe(true);
    expect(t.reached.size).toBe(0);
  });

  // A genuinely empty result set is complete information, not a gap.
  it("treats an empty result set as complete", () => {
    const body = JSON.stringify({ results: [] });
    const t = buildTrace(
      [log({ run: "run-1", stage: "plan", respBody: body, respBytes: body.length })],
      "run-1",
    );
    expect(t.truncated).toBe(false);
    expect(t.stages[0].queries[0].sources).toEqual([]);
  });

  it("keeps the query text for each retrieval", () => {
    const t = buildTrace(
      [
        log({
          run: "run-1",
          stage: "plan",
          reqBody: JSON.stringify({ query: "リトライ方針", k: 5 }),
          respBody: results(["a.md"]),
        }),
      ],
      "run-1",
    );
    expect(t.stages[0].queries[0].query).toBe("リトライ方針");
  });

  // Dropping an unattributed retrieval would make the stage totals disagree
  // with the run total.
  it("buckets retrievals with no stage rather than dropping them", () => {
    const t = buildTrace([log({ run: "run-1", respBody: results(["a.md"]) })], "run-1");
    expect(t.stages).toHaveLength(1);
    expect(t.reached.get("a.md")).toBe(1);
  });

  it("returns an empty trace for no run", () => {
    expect(buildTrace([log({ run: "run-1" })], "").stages).toEqual([]);
  });
});

describe("runIds", () => {
  it("lists runs most recent first, without duplicates", () => {
    expect(
      runIds([log({ run: "a" }), log({ run: "b" }), log({ run: "a" }), log({})]),
    ).toEqual(["a", "b"]);
  });
});

// The gateway serves its ring buffer newest-first. Taking the log's order would
// present the run backwards — on a screen for reading how a run progressed,
// that is worse than showing nothing. Caught against a real capture.
describe("ordering", () => {
  it("presents stages oldest-first even when the log is newest-first", () => {
    const t = buildTrace(
      [
        log({ run: "r", stage: "reviewer", time: "2026-08-01T00:00:30Z", respBody: results(["c.md"]) }),
        log({ run: "r", stage: "builder", time: "2026-08-01T00:00:20Z", respBody: results(["b.md"]) }),
        log({ run: "r", stage: "planner", time: "2026-08-01T00:00:10Z", respBody: results(["a.md"]) }),
      ],
      "r",
    );
    expect(t.stages.map((s) => s.id)).toEqual(["planner", "builder", "reviewer"]);
  });

  // Timestamps are second-granular, so a stage's own searches often share one.
  it("orders same-second queries forwards", () => {
    const t = buildTrace(
      [
        log({ run: "r", stage: "p", time: "2026-08-01T00:00:10Z", reqBody: JSON.stringify({ query: "second" }), respBody: results(["b.md"]) }),
        log({ run: "r", stage: "p", time: "2026-08-01T00:00:10Z", reqBody: JSON.stringify({ query: "first" }), respBody: results(["a.md"]) }),
      ],
      "r",
    );
    expect(t.stages[0].queries.map((q) => q.query)).toEqual(["first", "second"]);
  });
});

describe("following a search result", () => {
  const fetched = (source: string, extra: Partial<AccessLog> = {}) =>
    log({
      run: "run-1",
      stage: "build",
      path: "/rag/source",
      reqBody: JSON.stringify({ source, as: "text" }),
      respBody: JSON.stringify({ source, text: "…" }),
      ...extra,
    });

  it("counts a fetched source as reached", () => {
    const t = buildTrace([fetched("kon/character-bible.md")], "run-1");
    expect(t.reached.get("kon/character-bible.md")).toBe(1);
    expect(t.queryCount).toBe(1);
  });

  // The whole reason this matters: an image is indexed as metadata, so no
  // search ever returns its contents. Fetching the file is the only way a run
  // reaches a picture, and a trace blind to that would show the reference image
  // as untouched by a run that opened it.
  it("counts a fetched image, whose response is bytes rather than JSON", () => {
    const t = buildTrace(
      [
        fetched("kon/images/dog_sitting.JPEG", {
          reqBody: JSON.stringify({ source: "kon/images/dog_sitting.JPEG", as: "raw" }),
          respBody: "\x89PNG\r\n\x1a\n\x00\x00",
          respBytes: 4096,
        }),
      ],
      "run-1",
    );
    expect(t.reached.get("kon/images/dog_sitting.JPEG")).toBe(1);
    // Bytes that do not parse are not a truncated record: the name came from
    // the request, which is whole.
    expect(t.truncated).toBe(false);
  });

  it("merges searches and fetches into one stage's total", () => {
    const t = buildTrace(
      [
        log({ run: "run-1", stage: "build", respBody: results(["kon/bible.md"]) }),
        fetched("kon/bible.md"),
        fetched("kon/images/dog.JPEG"),
      ],
      "run-1",
    );
    expect(t.stages).toHaveLength(1);
    // Searched for it, then went and read it: the same source, reached twice.
    expect(t.reached.get("kon/bible.md")).toBe(2);
    expect(t.reached.get("kon/images/dog.JPEG")).toBe(1);
    expect(t.queryCount).toBe(3);
  });

  it("records the source it followed, so the two read as one sequence", () => {
    const t = buildTrace([fetched("kon/bible.md")], "run-1");
    expect(t.stages[0].queries[0].query).toBe("kon/bible.md");
  });

  it("reports an uncaptured request rather than inventing an empty fetch", () => {
    const t = buildTrace([fetched("x.md", { reqBody: undefined, reqBytes: 120 })], "run-1");
    expect(t.reached.size).toBe(0);
    expect(t.truncated).toBe(true);
  });
});

describe("passages a run received", () => {
  it("carries the text of each hit, with the stage and query that pulled it", () => {
    const t = buildTrace(
      [
        log({
          run: "run-1",
          stage: "plan",
          reqBody: JSON.stringify({ query: "コンの見た目" }),
          respBody: JSON.stringify({
            results: [
              { source: "kon/bible.md", text: "ネイビーのタートルネック", score: 0.81 },
              { source: "other.md", text: "無関係", score: 0.4 },
            ],
          }),
        }),
      ],
      "run-1",
    );
    const got = passagesFrom(t, "kon/bible.md");
    expect(got).toHaveLength(1);
    expect(got[0]).toMatchObject({ stage: "plan", query: "コンの見た目", text: "ネイビーのタートルネック", score: 0.81 });
    // Only that source's passages, not the whole response.
    expect(passagesFrom(t, "other.md")[0].text).toBe("無関係");
  });

  // A stage handed one chunk of a long document saw one chunk. Listing the
  // source twice because two queries returned it is the honest count.
  it("lists a passage per query, in the order they arrived", () => {
    const t = buildTrace(
      [
        log({ run: "run-1", stage: "plan", time: "2026-08-01T00:00:01Z", reqBody: JSON.stringify({ query: "first" }), respBody: JSON.stringify({ results: [{ source: "a.md", text: "one" }] }) }),
        log({ run: "run-1", stage: "plan", time: "2026-08-01T00:00:02Z", reqBody: JSON.stringify({ query: "second" }), respBody: JSON.stringify({ results: [{ source: "a.md", text: "two" }] }) }),
      ],
      "run-1",
    );
    expect(passagesFrom(t, "a.md").map((p) => p.text)).toEqual(["one", "two"]);
  });

  // A salvaged name and a byte body both reach a source without yielding text.
  // The source still counts as reached; there is simply nothing to read.
  it("omits hits with no readable text while keeping them reached", () => {
    const t = buildTrace(
      [
        log({
          run: "run-1",
          stage: "build",
          path: "/rag/source",
          reqBody: JSON.stringify({ source: "kon/images/dog.JPEG", as: "raw" }),
          respBody: "\x89PNG\r\n",
        }),
      ],
      "run-1",
    );
    expect(t.reached.get("kon/images/dog.JPEG")).toBe(1);
    expect(passagesFrom(t, "kon/images/dog.JPEG")).toHaveLength(0);
  });

  it("returns the document a text fetch answered with", () => {
    const t = buildTrace(
      [
        log({
          run: "run-1",
          stage: "build",
          path: "/rag/source",
          reqBody: JSON.stringify({ source: "kon/bible.md", as: "text" }),
          respBody: JSON.stringify({ source: "kon/bible.md", text: "全文がここに" }),
        }),
      ],
      "run-1",
    );
    expect(passagesFrom(t, "kon/bible.md")[0].text).toBe("全文がここに");
  });
});
