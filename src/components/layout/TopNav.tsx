import { useState } from "react";
import { NavLink } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useStore, type NotifItem } from "@/store/useStore";
import { languages } from "@/i18n";

// notifDotColor: tone wins (e.g. failed CI = red), else fall back to kind.
function notifDotColor(n: NotifItem): string {
  if (n.tone === "error") return "var(--red)";
  if (n.tone === "ok") return "var(--green)";
  if (n.tone === "info") return "var(--ac)";
  return n.kind === "ci" ? "var(--green)" : n.kind === "artifact" ? "var(--cyan)" : "var(--ac)";
}

type TFn = ReturnType<typeof useTranslation>["t"];

// relativeTime formats an epoch-ms timestamp as a short relative string.
function relativeTime(ts: number, t: TFn): string {
  const s = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (s < 60) return t("nav.justNow");
  const m = Math.floor(s / 60);
  if (m < 60) return t("nav.minutesAgo", { count: m });
  const h = Math.floor(m / 60);
  if (h < 24) return t("nav.hoursAgo", { count: h });
  return t("nav.daysAgo", { count: Math.floor(h / 24) });
}

// Screen names stay untranslated on purpose: they are the product's own
// vocabulary (and the route paths), not descriptions of it.
const tabs = [
  { to: "/delivery", key: "nav.delivery" },
  { to: "/daily", key: "nav.daily" },
  { to: "/terminal", key: "nav.terminal" },
  { to: "/knowledge", key: "nav.knowledge" },
  { to: "/audit", key: "nav.audit" },
  { to: "/settings", key: "nav.settings" },
];

function tabStyle(active: boolean): React.CSSProperties {
  return active
    ? { font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)", padding: "6px 12px", background: "var(--bg-tab)", borderRadius: 7, cursor: "pointer" }
    : { font: "500 12.5px 'IBM Plex Sans'", color: "var(--tx-navoff)", padding: "6px 12px", cursor: "pointer" };
}

export function TopNav() {
  const { t } = useTranslation();
  const theme = useStore((s) => s.theme);
  const toggleTheme = useStore((s) => s.toggleTheme);
  const language = useStore((s) => s.language);
  const setLanguage = useStore((s) => s.setLanguage);
  const notifOpen = useStore((s) => s.notifOpen);
  const toggleNotif = useStore((s) => s.toggleNotif);
  const notifications = useStore((s) => s.notifications);
  const markAllRead = useStore((s) => s.markAllRead);
  const unread = notifications.filter((n) => !n.read).length;
  const [langOpen, setLangOpen] = useState(false);

  return (
    <div style={{ height: 50, flex: "none", background: "var(--bg-panel)", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", padding: "0 16px", gap: 18 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
        <div style={{ width: 22, height: 22, borderRadius: 6, background: "linear-gradient(135deg,#4f9dff,#34d3e0)", display: "flex", alignItems: "center", justifyContent: "center", boxShadow: "0 1px 3px rgba(79,157,255,0.35)" }}>
          {/* moeca — lowercase "m" monogram (also nods to Moue) */}
          <svg width="15" height="15" viewBox="0 0 22 22" fill="none" aria-label="moeca">
            <path
              d="M6.5 15.5 L6.5 9.6 C6.5 7 11 7 11 9.6 L11 15.5 M11 9.6 C11 7 15.5 7 15.5 9.6 L15.5 15.5"
              stroke="#ffffff"
              strokeWidth="2.1"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </div>
        <span style={{ font: "700 13.5px 'IBM Plex Sans'", color: "var(--tx)", letterSpacing: "-0.2px" }}>moeca</span>
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: 3, marginLeft: 6 }}>
        {tabs.map((tab) => (
          <NavLink key={tab.to} to={tab.to} style={({ isActive }) => tabStyle(isActive)}>
            {t(tab.key)}
          </NavLink>
        ))}
      </div>

      <div style={{ flex: 1 }} />

      {/* notification bell */}
      <div style={{ position: "relative" }}>
        <div onClick={toggleNotif} title={t("nav.notifications")} style={{ width: 30, height: 30, borderRadius: 8, border: "1px solid var(--bd2)", background: "var(--bg-card2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", position: "relative" }}>
          <svg width="15" height="15" viewBox="0 0 18 18" fill="none" stroke="var(--tx3)" strokeWidth="1.6"><path d="M9 2.5a4.5 4.5 0 0 0-4.5 4.5c0 4-1.5 5-1.5 5h12s-1.5-1-1.5-5A4.5 4.5 0 0 0 9 2.5z" /><path d="M7.5 15a1.5 1.5 0 0 0 3 0" /></svg>
          {unread > 0 && (
            <div style={{ position: "absolute", top: 4, right: 5, minWidth: 14, height: 14, padding: "0 3px", borderRadius: 7, background: "#e0654e", border: "1.5px solid var(--bg-panel)", display: "flex", alignItems: "center", justifyContent: "center", font: "600 8.5px 'IBM Plex Mono'", color: "#fff" }}>{unread}</div>
          )}
        </div>
        {notifOpen && (
          <>
            <div style={{ position: "fixed", inset: 0, zIndex: 55 }} onClick={toggleNotif} />
            <div style={{ position: "absolute", top: 38, right: 0, width: 340, background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 11, boxShadow: "0 18px 50px rgba(0,0,0,.4)", zIndex: 60, overflow: "hidden" }}>
              <div style={{ padding: "12px 15px", borderBottom: "1px solid var(--bd)", display: "flex", alignItems: "center", gap: 9 }}>
                <span style={{ font: "700 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("nav.notifications")}</span>
                <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "#e0654e", background: "var(--tint-red)", padding: "2px 6px", borderRadius: 5 }}>{t("nav.unreadCount", { count: unread })}</span>
                <div style={{ flex: 1 }} />
                <div onClick={markAllRead} style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--ac)", cursor: "pointer" }}>{t("nav.markAllRead")}</div>
              </div>
              <div style={{ maxHeight: 380, overflowY: "auto" }}>
                {notifications.map((n) => (
                  <div key={n.id} style={{ display: "flex", gap: 10, padding: "11px 15px", borderBottom: "1px solid var(--bd3)", cursor: "pointer", background: n.read ? "transparent" : "var(--bg-card)" }}>
                    <div style={{ marginTop: 5, width: 7, height: 7, borderRadius: "50%", flex: "none", background: notifDotColor(n) }} />
                    <div style={{ display: "flex", flexDirection: "column", gap: 3, minWidth: 0, flex: 1 }}>
                      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                        <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx3)", textTransform: "uppercase", letterSpacing: "0.4px" }}>{n.kind}</span>
                        <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", marginLeft: "auto" }}>{n.ts ? relativeTime(n.ts, t) : n.time}</span>
                      </div>
                      <span style={{ font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", lineHeight: 1.4 }}>{n.title}</span>
                      <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{n.detail}</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          </>
        )}
      </div>

      {/* language picker — the current language shows as its own two-letter
          code so it stays readable whichever language the UI is in. */}
      <div style={{ position: "relative" }}>
        <div
          onClick={() => setLangOpen((v) => !v)}
          title={t("nav.language")}
          style={{ height: 30, padding: "0 9px", borderRadius: 8, border: "1px solid var(--bd2)", background: "var(--bg-card2)", display: "flex", alignItems: "center", gap: 5, cursor: "pointer" }}
        >
          <svg width="14" height="14" viewBox="0 0 18 18" fill="none" stroke="var(--tx3)" strokeWidth="1.5"><circle cx="9" cy="9" r="6.75" /><path d="M2.25 9h13.5M9 2.25c1.7 1.9 2.6 4.2 2.6 6.75S10.7 13.85 9 15.75C7.3 13.85 6.4 11.55 6.4 9S7.3 4.15 9 2.25z" /></svg>
          <span style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--tx3)", textTransform: "uppercase", letterSpacing: "0.4px" }}>{language}</span>
        </div>
        {langOpen && (
          <>
            <div style={{ position: "fixed", inset: 0, zIndex: 55 }} onClick={() => setLangOpen(false)} />
            <div style={{ position: "absolute", top: 38, right: 0, minWidth: 148, background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 10, boxShadow: "0 18px 50px rgba(0,0,0,.4)", zIndex: 60, overflow: "hidden", padding: 4 }}>
              {languages.map((l) => (
                <div
                  key={l.code}
                  onClick={() => { setLanguage(l.code); setLangOpen(false); }}
                  style={{ display: "flex", alignItems: "center", gap: 8, padding: "8px 10px", borderRadius: 7, cursor: "pointer", background: l.code === language ? "var(--tint-active)" : "transparent" }}
                >
                  <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", textTransform: "uppercase", width: 16 }}>{l.code}</span>
                  <span style={{ font: "500 12px 'IBM Plex Sans'", color: l.code === language ? "var(--tx)" : "var(--tx2)" }}>{l.label}</span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>

      <div onClick={toggleTheme} title={t("nav.toggleTheme")} style={{ width: 30, height: 30, borderRadius: 8, border: "1px solid var(--bd2)", background: "var(--bg-card2)", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer" }}>
        {theme === "dark" ? (
          <svg width="15" height="15" viewBox="0 0 18 18" fill="none" stroke="var(--tx3)" strokeWidth="1.5"><path d="M15 10.5A6 6 0 1 1 7.5 3a4.5 4.5 0 0 0 7.5 7.5z" /></svg>
        ) : (
          <svg width="15" height="15" viewBox="0 0 18 18" fill="none" stroke="var(--tx3)" strokeWidth="1.5"><circle cx="9" cy="9" r="3.5" /><path d="M9 1v2M9 15v2M1 9h2M15 9h2M3.5 3.5l1.4 1.4M13.1 13.1l1.4 1.4M3.5 14.5l1.4-1.4M13.1 4.9l1.4-1.4" /></svg>
        )}
      </div>
      <div style={{ width: 27, height: 27, borderRadius: "50%", background: "linear-gradient(135deg,#4f9dff,#34d3e0)", border: "1px solid var(--bd-sep)" }} />
    </div>
  );
}
