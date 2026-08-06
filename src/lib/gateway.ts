// Client for the Orchestra egress gateway's observability (the monitoring plane).
//
// logs/metrics are ADMIN-gated on the gateway (they include captured prompt /
// response content), so they are read through Tauri commands — the admin token
// stays in Rust and a sandbox (which only holds a session token) can never read
// the content. In a plain browser (no desktop shell) these return empty.

type Invoke = <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>;
function invoker(): Invoke | null {
  const g = (window as unknown as { __TAURI__?: { core?: { invoke?: Invoke } } }).__TAURI__;
  return g?.core?.invoke ?? null;
}

// AccessLog mirrors the gateway's structured record (one per request), now with
// run/stage attribution and captured request/response content.
export interface AccessLog {
  time: string;
  requestId: string;
  session: string;
  run?: string;
  stage?: string;
  service: string;
  model?: string;
  method: string;
  path: string;
  upstream?: string;
  status: number;
  reqBytes: number;
  respBytes: number;
  reqBody?: string;
  respBody?: string;
  durationMs: number;
  tokensEst?: number;
  inputTokens?: number;
  outputTokens?: number;
  err?: string;
}

export interface ServiceMetrics {
  requests: number;
  tokensEst: number;
}

export interface GatewayMetrics {
  totalRequests: number;
  totalTokensEst: number;
  sessions: number;
  perService: Record<string, ServiceMetrics>;
}

export const gateway = {
  async health(): Promise<boolean> {
    return invoker() != null; // desktop shell present ⇒ gateway is managed
  },
  async logs(): Promise<AccessLog[]> {
    const inv = invoker();
    if (!inv) return [];
    const r = await inv<{ logs: AccessLog[] }>("gateway_logs");
    return r.logs ?? [];
  },
  async metrics(): Promise<GatewayMetrics | null> {
    const inv = invoker();
    if (!inv) return null;
    return inv<GatewayMetrics>("gateway_metrics");
  },
};
