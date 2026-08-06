// Provider (LLM connection) client — all calls go through Tauri commands so the
import i18n from "@/i18n";
// admin token stays in Rust and secrets never touch the webview/localStorage.
// In a plain browser (Vite dev without the desktop shell) these are no-ops.

export interface ProviderInput {
  name: string;
  kind: string; // "model" | "tool"
  /** Agent wire format for model providers: "anthropic" | "openai" | "gemini".
   * Gateway-agnostic (it just proxies); used client-side to pick the dialect. */
  dialect?: string;
  prefix: string; // gateway route, e.g. "/openai/"
  upstream: string;
  allowlist: string[];
  models: string[];
  injectHeaders: Record<string, string>;
  /** Per-session token budget (0 => unlimited). Omit to preserve the gateway's
   * current value; set to enforce/raise/clear a hard spend ceiling (402 on
   * exceed). Model providers only. */
  maxTokensPerSession?: number;
}

/** The gateway's secret-free view of a provider (adds hasSecret). */
export interface ProviderView extends ProviderInput {
  hasSecret: boolean;
}

type Invoke = <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>;

function invoker(): Invoke | null {
  const g = (window as unknown as { __TAURI__?: { core?: { invoke?: Invoke } } }).__TAURI__;
  return g?.core?.invoke ?? null;
}

/** True inside the Tauri desktop shell (provider management available). */
export function isDesktop(): boolean {
  return invoker() != null;
}

async function inv<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const f = invoker();
  if (!f) throw new Error(i18n.t("errors.providersDesktopOnly"));
  return f<T>(cmd, args);
}

function unwrap(r: { providers: ProviderView[] }): ProviderView[] {
  return r.providers ?? [];
}

export const providersApi = {
  /** Apply the full non-secret provider set to the gateway; returns live views. */
  sync: (list: ProviderInput[]) =>
    inv<{ providers: ProviderView[] }>("provider_sync", { providers: list }).then(unwrap),
  list: () => inv<{ providers: ProviderView[] }>("provider_list").then(unwrap),
  /** Store an API key (write-only) in the keychain and push it to the gateway. */
  setSecret: (name: string, value: string) =>
    inv<{ providers: ProviderView[] }>("provider_set_secret", { name, value }).then(unwrap),
  remove: (name: string) =>
    inv<{ providers: ProviderView[] }>("provider_delete", { name }).then(unwrap),
};
