import { Component, type ErrorInfo, type ReactNode } from "react";
// A class component cannot hold a hook, and this one renders only after a
// crash — reading the active language off the i18n singleton at render time is
// enough, and keeps the boundary itself dependency-free.
import i18n from "@/i18n";

interface Props {
  /** remounts the boundary (clearing the error) when it changes — e.g. the route key */
  resetKey?: string;
  children: ReactNode;
}

interface State {
  error: Error | null;
}

// ScreenBoundary keeps a render error inside one screen instead of unmounting
// the whole app. Navigating elsewhere changes resetKey and clears the error.
export class ScreenBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidUpdate(prev: Props) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("[screen]", error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    return (
      <div style={{ flex: 1, minWidth: 0, overflowY: "auto", padding: "40px 32px", display: "flex", flexDirection: "column", gap: 14, alignItems: "flex-start" }}>
        <span style={{ font: "600 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{i18n.t("errors.screenTitle")}</span>
        <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>
          {i18n.t("errors.screenBody")}
        </span>
        <pre style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "10px 13px", maxWidth: "100%", whiteSpace: "pre-wrap", wordBreak: "break-word", margin: 0 }}>
          {error.message}
        </pre>
        <div
          onClick={() => this.setState({ error: null })}
          style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer", padding: "6px 13px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)" }}
        >
          {i18n.t("common.retry")}
        </div>
      </div>
    );
  }
}
