import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import type { SoloAgent } from "@/lib/templates";
import { Avatar, SearchIcon, searchBox, searchInput } from "./ui";

/**
 * Choosing the agent a conversation talks to.
 *
 * The list is Solos only, and that is a decision rather than a limitation. A
 * Static or Graph template is a pipeline — plan → build → review, a fresh
 * container per stage, terminating — which makes a fine way to have a piece of
 * work done and a poor way to hold a conversation: every turn re-instantiates the
 * team, the team's internal exchange never becomes part of the history, and three
 * stages mean three container starts before the person sees a word. A Solo has one
 * message stream, which is what a conversation is.
 *
 * Nothing here prevents binding a template later: the conversation stores a
 * template REF, and compileRef already handles every shape. Widening this list is
 * where that would start — most likely as "delegate this one turn to a team"
 * rather than as a conversation-wide binding.
 */
export function AgentPicker({
  agents,
  activeId,
  onPick,
  onClose,
}: {
  agents: SoloAgent[];
  activeId: string | undefined;
  onPick: (id: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [q, setQ] = useState("");

  const needle = q.trim().toLowerCase();
  const list = needle
    ? agents.filter((a) => `${a.name} ${a.role}`.toLowerCase().includes(needle))
    : agents;

  return (
    <div
      onClick={onClose}
      style={{
        animation: "ocFade .16s ease-out both", position: "absolute", inset: 0, zIndex: 40,
        background: "rgba(4,6,9,.55)", display: "flex", alignItems: "center", justifyContent: "center", padding: 24,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          animation: "ocPop .2s cubic-bezier(.2,.8,.2,1) both", width: 520, maxWidth: "100%", maxHeight: "100%",
          background: "var(--bg-panel)", borderRadius: 20, boxShadow: "0 24px 70px rgba(0,0,0,.5)",
          display: "flex", flexDirection: "column", overflow: "hidden",
        }}
      >
        <div style={{ padding: "22px 24px 6px", display: "flex", alignItems: "flex-start", gap: 12 }}>
          <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: 5 }}>
            <span style={{ font: "600 15px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("chat.picker.title")}</span>
            <span style={{ font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>
              {t("chat.picker.desc")}
            </span>
          </div>
          <div onClick={onClose} style={{ width: 30, height: 30, borderRadius: "50%", display: "flex", alignItems: "center", justifyContent: "center", cursor: "pointer", color: "var(--tx-dim)", font: "400 15px 'IBM Plex Sans'" }}>
            ✕
          </div>
        </div>

        <div style={{ padding: "12px 24px 6px" }}>
          <div style={{ ...searchBox, borderRadius: 10, padding: "9px 12px" }}>
            <SearchIcon />
            <input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={t("chat.picker.search")}
              style={{ ...searchInput, font: "400 12px 'IBM Plex Sans'" }}
            />
          </div>
        </div>

        <div style={{ flex: 1, overflowY: "auto", padding: "8px 14px 10px", display: "flex", flexDirection: "column", gap: 2 }}>
          {list.map((a) => {
            const on = a.id === activeId;
            return (
              <div
                key={a.id}
                onClick={() => { onPick(a.id); onClose(); }}
                style={{
                  display: "flex", alignItems: "center", gap: 12, padding: "11px 10px", borderRadius: 12,
                  cursor: "pointer", background: on ? "var(--bg-card)" : "transparent",
                }}
              >
                <Avatar agent={a} size={34} />
                <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 4 }}>
                  <div style={{ display: "flex", alignItems: "baseline", gap: 8 }}>
                    <span style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)" }}>{a.name}</span>
                    <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{a.role}</span>
                  </div>
                  <div style={{ display: "flex", alignItems: "center", gap: 10, font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)", minWidth: 0 }}>
                    <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{a.model}</span>
                    {a.ctx && <span>ctx {a.ctx}</span>}
                    {a.strat && <span style={{ color: "var(--tx-dim)" }}>{a.strat}</span>}
                  </div>
                </div>
                <div
                  style={{
                    width: 20, height: 20, flex: "none", borderRadius: "50%",
                    background: on ? "var(--ac)" : "transparent",
                    border: on ? "none" : "1px solid var(--bd2)",
                    display: "flex", alignItems: "center", justifyContent: "center",
                  }}
                >
                  {on && (
                    <svg width="11" height="11" viewBox="0 0 16 16" fill="none" stroke="#06121e" strokeWidth="2.4">
                      <path d="m3.5 8.5 3 3 6-7" />
                    </svg>
                  )}
                </div>
              </div>
            );
          })}
          {list.length === 0 && (
            <span style={{ font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "18px 10px" }}>
              {agents.length === 0 ? t("chat.picker.none") : t("chat.picker.empty")}
            </span>
          )}
        </div>

        <div style={{ padding: "12px 24px 18px", display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ flex: 1, font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("chat.picker.hint")}</span>
          <div
            onClick={() => navigate("/settings")}
            style={{ font: "600 11px 'IBM Plex Sans'", color: "var(--tx3)", background: "var(--bg-card)", padding: "7px 13px", borderRadius: 16, cursor: "pointer" }}
          >
            {t("chat.picker.open")}
          </div>
        </div>
      </div>
    </div>
  );
}
