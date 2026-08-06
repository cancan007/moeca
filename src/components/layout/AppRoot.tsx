import { useEffect } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { useStore } from "@/store/useStore";
import { isDesktop } from "@/lib/providers";
import { githubApp } from "@/lib/githubApp";
import { TopNav } from "./TopNav";
import { ScreenBoundary } from "./ScreenBoundary";

export function AppRoot() {
  const { pathname } = useLocation();
  const theme = useStore((s) => s.theme);
  const syncProviders = useStore((s) => s.syncProviders);
  const source = useStore((s) => s.source);
  const refreshLive = useStore((s) => s.refreshLive);
  const autoConnectHostAgent = useStore((s) => s.autoConnectHostAgent);

  // On launch (desktop only), apply the persisted providers + keychain secrets
  // to the gateway and pull back the live view (hasSecret flags).
  useEffect(() => {
    if (isDesktop()) void syncProviders();
  }, [syncProviders]);

  // On launch (desktop only), auto-connect to the host agent so Delivery/Daily
  // open on live data instead of mock. Best-effort: stays mock if it isn't up.
  useEffect(() => {
    if (isDesktop()) void autoConnectHostAgent();
  }, [autoConnectHostAgent]);

  // Once the host agent is up (source live), re-push any stored GitHub App
  // credentials to it — the host agent holds them in memory only.
  useEffect(() => {
    if (source === "live" && isDesktop()) void githubApp.resync().catch(() => {});
  }, [source]);

  // While connected to the host agent, poll live tasks app-wide so CI
  // transitions raise notifications even when the user isn't on the board.
  useEffect(() => {
    if (source !== "live") return;
    const t = window.setInterval(() => void refreshLive(), 5000);
    return () => window.clearInterval(t);
  }, [source, refreshLive]);

  return (
    <div
      id="app-root"
      data-theme={theme}
      style={{
        width: "100vw",
        height: "100vh",
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        background: "var(--bg-app)",
        color: "var(--tx)",
      }}
    >
      <TopNav />
      <div style={{ flex: 1, display: "flex", minHeight: 0, position: "relative" }}>
        <ScreenBoundary resetKey={pathname}>
          <Outlet />
        </ScreenBoundary>
      </div>
    </div>
  );
}
