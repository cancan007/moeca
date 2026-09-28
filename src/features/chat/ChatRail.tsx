import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { Conversation } from "@/lib/chat";
import { PanelIcon, SearchIcon, iconButton, searchBox, searchInput } from "./ui";

// The conversation list.
//
// Grouped by when a conversation was last touched rather than sorted flat,
// because "which of these was I in this morning" is the question the list is
// actually asked. The counts are not drawn: a date heading with "(4)" after it is
// information nobody wanted.

type Bucket = "today" | "yesterday" | "week" | "older";

function bucketOf(iso: string, now: Date): Bucket {
  // SQLite's datetime('now') is UTC without a zone marker, which Date would read
  // as local time and put half a day out. Treating a zoneless stamp as UTC is
  // what makes "today" mean today.
  const stamp = /[zZ]|[+-]\d\d:?\d\d$/.test(iso) ? iso : iso.replace(" ", "T") + "Z";
  const then = new Date(stamp);
  if (Number.isNaN(then.getTime())) return "older";
  const startOf = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((startOf(now) - startOf(then)) / 86_400_000);
  if (days <= 0) return "today";
  if (days === 1) return "yesterday";
  if (days <= 7) return "week";
  return "older";
}

const ORDER: Bucket[] = ["today", "yesterday", "week", "older"];

/** Held at full width inside the collapsing shell, so the list does not re-wrap
 *  on every frame while the rail opens. */
const RAIL_W = 260;

export function ChatRail({
  conversations,
  activeId,
  open,
  onToggle,
  onSelect,
  onNew,
  onRemove,
}: {
  conversations: Conversation[];
  activeId: string | null;
  open: boolean;
  onToggle: () => void;
  onSelect: (id: string) => void;
  onNew: () => void;
  onRemove: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [q, setQ] = useState("");
  const [hover, setHover] = useState<string | null>(null);

  const groups = useMemo(() => {
    const now = new Date();
    const needle = q.trim().toLowerCase();
    const rows = needle
      ? conversations.filter((c) => `${c.title} ${c.lastText}`.toLowerCase().includes(needle))
      : conversations;
    return ORDER.map((b) => ({ bucket: b, rows: rows.filter((c) => bucketOf(c.updatedAt, now) === b) }))
      .filter((g) => g.rows.length > 0);
  }, [conversations, q]);

  return (
    <div
      style={{
        width: open ? RAIL_W : 0, flex: "none", overflow: "hidden",
        // The border is on the inner element, not here: toggling `borderRight`
        // between "1px" and "none" changes the box width by a pixel at the start
        // and end of the transition, which reads as a twitch.
        background: "var(--bg-panel)", display: "flex", flexDirection: "column", minHeight: 0,
        transition: "width .22s cubic-bezier(.2,.8,.2,1)",
      }}
    >
      <div
        style={{
          width: RAIL_W, flex: 1, display: "flex", flexDirection: "column", minHeight: 0,
          borderRight: "1px solid var(--bd)",
          opacity: open ? 1 : 0,
          transition: `opacity .18s cubic-bezier(.2,.8,.2,1) ${open ? ".04s" : "0s"}`,
        }}
      >
        <div style={{ padding: "14px 14px 8px", display: "flex", flexDirection: "column", gap: 10 }}>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", padding: "0 2px 2px 6px" }}>
            <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx2)" }}>{t("chat.rail.title")}</span>
            <div onClick={onToggle} title={t("chat.rail.close")} style={iconButton()}>
              <PanelIcon />
            </div>
          </div>
          <div style={searchBox}>
            <SearchIcon />
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("chat.rail.search")} style={searchInput} />
          </div>
          <div
            onClick={onNew}
            style={{ display: "flex", alignItems: "center", gap: 9, font: "500 12px 'IBM Plex Sans'", color: "var(--tx2)", borderRadius: 9, padding: "8px 11px", cursor: "pointer" }}
          >
            <span style={{ font: "400 16px 'IBM Plex Sans'", lineHeight: 1, color: "var(--tx3)", width: 14, textAlign: "center" }}>+</span>
            {t("chat.rail.new")}
          </div>
        </div>

        <div style={{ flex: 1, overflowY: "auto", padding: "4px 10px 18px", display: "flex", flexDirection: "column", gap: 1 }}>
          {groups.map((g) => (
            <div key={g.bucket} style={{ display: "flex", flexDirection: "column", gap: 1 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 7, padding: "18px 11px 7px" }}>
                <span style={{ font: "600 9.5px 'IBM Plex Mono'", letterSpacing: ".1em", color: "var(--tx-faint)" }}>
                  {t(`chat.groups.${g.bucket}`)}
                </span>
              </div>
              {g.rows.map((c) => {
                const on = c.id === activeId;
                return (
                  <div
                    key={c.id}
                    onClick={() => onSelect(c.id)}
                    onMouseEnter={() => setHover(c.id)}
                    onMouseLeave={() => setHover((h) => (h === c.id ? null : h))}
                    style={{
                      display: "flex", alignItems: "center", gap: 10, padding: "8px 11px", borderRadius: 9,
                      cursor: "pointer", background: on ? "var(--bg-card)" : hover === c.id ? "var(--bg-card2)" : "transparent",
                    }}
                  >
                    <div
                      style={{
                        width: 6, height: 6, borderRadius: "50%", flex: "none",
                        background: on ? "var(--ac)" : "var(--bd-sep)",
                      }}
                    />
                    <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 2 }}>
                      <span
                        style={{
                          font: `${on ? 600 : 500} 12px 'IBM Plex Sans'`, color: on ? "var(--tx)" : "var(--tx2)",
                          overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
                        }}
                      >
                        {c.title || t("chat.untitled")}
                      </span>
                      <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {c.lastText || t("chat.rail.noMessages")}
                      </span>
                    </div>
                    {hover === c.id ? (
                      <div
                        onClick={(e) => { e.stopPropagation(); onRemove(c.id); }}
                        title={t("chat.rail.delete")}
                        style={{ ...iconButton(), width: 22, height: 22, font: "400 11px 'IBM Plex Sans'" }}
                      >
                        ✕
                      </div>
                    ) : (
                      c.turnCount > 0 && (
                        <span style={{ font: "500 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{c.turnCount}</span>
                      )
                    )}
                  </div>
                );
              })}
            </div>
          ))}
          {groups.length === 0 && (
            <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "18px 11px", lineHeight: 1.7 }}>
              {conversations.length === 0 ? t("chat.rail.empty") : t("chat.rail.noMatch")}
            </span>
          )}
        </div>
      </div>
    </div>
  );
}
