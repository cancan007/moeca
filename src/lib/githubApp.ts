// Host-side GitHub App credentials (Delivery issue pull). All calls go through
import i18n from "@/i18n";
// Tauri commands so the private key stays in Rust/keychain and never touches the
// webview/localStorage. In a plain browser these throw (desktop-only).

type Invoke = <T>(cmd: string, args?: Record<string, unknown>) => Promise<T>;

function invoker(): Invoke | null {
  const g = (window as unknown as { __TAURI__?: { core?: { invoke?: Invoke } } }).__TAURI__;
  return g?.core?.invoke ?? null;
}

async function inv<T>(cmd: string, args?: Record<string, unknown>): Promise<T> {
  const f = invoker();
  if (!f) throw new Error(i18n.t("errors.githubAppDesktopOnly"));
  return f<T>(cmd, args);
}

export interface GitHubAppStatus {
  configured: boolean;
  appId?: string;
  pushed?: boolean;
}

export const githubApp = {
  status: () => inv<GitHubAppStatus>("github_app_status"),
  /** Store App ID + PEM private key (keychain) and push to the host agent, which
   * validates the key. Rejects if the host agent can't parse it. */
  set: (appId: string, privateKey: string) => inv<GitHubAppStatus>("github_app_set", { appId, privateKey }),
  clear: () => inv<GitHubAppStatus>("github_app_clear"),
  /** Re-push stored credentials to the host agent (held in memory only). */
  resync: () => inv<GitHubAppStatus>("github_app_resync"),
};
