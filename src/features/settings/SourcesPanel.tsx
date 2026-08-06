import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { sectionTitle } from "./ui";
import { daily, type SourceType } from "@/lib/daily";

const TYPES: SourceType[] = ["jira", "trello", "notion"];

// SourcesPanel manages the Daily pull providers (Jira / Trello / Notion). The
// host agent persists them and rebuilds its registry; the adapters route
// through the gateway (which injects each provider's credentials), so no keys
// live here. Configure upstream + secret per provider in Proxy / gateway.json.
export function SourcesPanel() {
  const { t } = useTranslation();
  const [sources, setSources] = useState<string[]>([]);
  const [online, setOnline] = useState<boolean | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [type, setType] = useState<SourceType>("jira");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  const refresh = async () => {
    try {
      setSources(await daily.sources());
      setOnline(true);
      setErr(null);
    } catch (e) {
      setOnline(false);
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  useEffect(() => {
    refresh();
  }, []);

  const add = async () => {
    setBusy(true);
    try {
      await daily.addSource(type, name.trim() || undefined);
      setName("");
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (s: string) => {
    setBusy(true);
    try {
      await daily.removeSource(s);
      await refresh();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
      {sectionTitle(t("settings.sources.title"), t("settings.sources.desc"))}

      {online === false && (
        <div style={{ font: "400 11px 'IBM Plex Sans'", color: "#d39a4e", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "10px 13px" }}>
          {t("errors.hostAgentOfflineDesktop")}
        </div>
      )}
      {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)", background: "var(--bg-card)", border: "1px solid var(--tint-red-bd)", borderRadius: 9, padding: "9px 12px" }}>{err}</div>}

      {/* add */}
      <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "14px 16px", display: "flex", alignItems: "center", gap: 10 }}>
        <select
          value={type}
          onChange={(e) => setType(e.target.value as SourceType)}
          style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 10px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }}
        >
          {TYPES.map((t) => (
            <option key={t} value={t}>{t}</option>
          ))}
        </select>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t("settings.sources.namePlaceholder")}
          style={{ flex: 1, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none" }}
        />
        <div
          onClick={() => !busy && add()}
          style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 14px", borderRadius: 7, cursor: busy ? "default" : "pointer", opacity: busy ? 0.6 : 1 }}
        >
          {t("common.add")}
        </div>
      </div>

      {/* list */}
      <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
        {sources.length === 0 && (
          <div style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-faint)", padding: "4px 2px" }}>{t("settings.sources.none")}</div>
        )}
        {sources.map((s) => (
          <div key={s} style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 10, padding: "11px 14px", display: "flex", alignItems: "center", gap: 10 }}>
            <div style={{ width: 7, height: 7, borderRadius: "50%", background: "#b08ad9", flex: "none" }} />
            <span style={{ font: "600 12px 'IBM Plex Mono'", color: "var(--tx2)" }}>{s}</span>
            <div style={{ flex: 1 }} />
            <div
              onClick={() => !busy && remove(s)}
              title={t("common.delete")}
              style={{ font: "500 10.5px 'IBM Plex Sans'", color: "var(--red)", cursor: busy ? "default" : "pointer", padding: "5px 10px", border: "1px solid var(--tint-red-bd)", borderRadius: 7, background: "var(--tint-red)" }}
            >
              {t("common.delete")}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
