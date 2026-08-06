import { useCallback, useEffect, useMemo, useState } from "react";
import i18n from "@/i18n";
import { useSearchParams } from "react-router-dom";
import { gateway, type AccessLog } from "@/lib/gateway";
import { sandbox, type RunStatus } from "@/lib/sandbox";
import { buildTrace, EMPTY_TRACE, type Trace } from "./trace";

// The trace a run left in the knowledge base.
//
// The run id lives in the URL rather than in component state, so Audit can hand
// one over by navigating and the result is a link the user can keep. Clearing
// the trace clears the parameter, which is why the screen never needs to
// distinguish "no trace requested" from "trace cleared".
//
// Both sources are read once when a run is named and not polled. A finished run
// is not going to grow more retrievals, and a live one refetching under a user
// who is reading its stages would move the ground under them.

export interface TraceData {
  runId: string;
  trace: Trace;
  /** the run's declared stages, so a stage that reached nothing still shows. */
  stages: RunStatus["stages"];
  error: string;
  loading: boolean;
  setRun: (id: string | null) => void;
}

export function useTrace(): TraceData {
  const [params, setParams] = useSearchParams();
  const runId = params.get("run") ?? "";
  const [logs, setLogs] = useState<AccessLog[]>([]);
  const [stages, setStages] = useState<RunStatus["stages"]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!runId) {
      setLogs([]);
      setStages([]);
      setError("");
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    Promise.allSettled([gateway.logs(), sandbox.runStatus(runId)])
      .then(([l, r]) => {
        if (cancelled) return;
        if (l.status === "fulfilled") {
          setLogs(l.value);
          // Outside the desktop shell the gateway's log route is unreachable —
          // the admin token lives in Rust — so an empty result there is a
          // capability gap, not a run that did nothing.
          if (l.value.length === 0) {
            setError(i18n.t("knowledge.gatewayLogUnreadable"));
          }
        } else {
          setError(String(l.reason));
        }
        // The run status may be gone — archives are pruned on a retention
        // schedule — while the gateway log survives. The trace still works; it
        // just cannot name stages that retrieved nothing.
        setStages(r.status === "fulfilled" ? (r.value.stages ?? []) : []);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [runId]);

  const trace = useMemo(() => (runId ? buildTrace(logs, runId) : EMPTY_TRACE), [logs, runId]);

  const setRun = useCallback(
    (id: string | null) => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (id) next.set("run", id);
          else next.delete("run");
          return next;
        },
        { replace: true },
      );
    },
    [setParams],
  );

  return { runId, trace, stages, error, loading, setRun };
}
