import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { DeliveryTask } from "@/store/useStore";

const vrt = [
  { story: "SearchBar / default", diff: "0.0%", ok: true },
  { story: "SearchBar / loading", diff: "0.0%", ok: true },
  { story: "ResultList / dense", diff: "1.8%", ok: false },
];

const api = [
  { method: "POST", mcolor: "var(--green)", path: "/v1/index/rebuild", status: "200", scolor: "var(--green)", ms: "142ms", req: `{\n  "scope": "full",\n  "batch": 500\n}`, res: `{\n  "indexed": 12840,\n  "took_ms": 142,\n  "ok": true\n}` },
  { method: "GET", mcolor: "var(--ac)", path: "/v1/search?q=retry", status: "200", scolor: "var(--green)", ms: "38ms", req: `Accept: application/json\nAuthorization: Bearer ****`, res: `{\n  "hits": 24,\n  "took_ms": 38\n}` },
];

export function EvidencePane({ task }: { task: DeliveryTask }) {
  const { t } = useTranslation();
  const [full, setFull] = useState(false);
  const isVrt = task.evidence === "vrt";
  const grid: React.CSSProperties = { display: "grid", gridTemplateColumns: full ? "1fr" : "1fr 1fr", gap: 12 };

  return (
    <div style={{ display: "flex", flexDirection: "column" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "11px 16px", borderBottom: "1px solid var(--bd)" }}>
        <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.5px" }}>EVIDENCE</span>
        <div style={{ display: "flex", alignItems: "center", gap: 7, background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 6, padding: "3px 9px" }}>
          <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "#5b9fe8" }}>{isVrt ? "frontend" : "backend"}</span>
          <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>→</span>
          <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx2)" }}>{isVrt ? "Storybook VRT" : "API req/res"}</span>
        </div>
        <div style={{ flex: 1 }} />
        <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{isVrt ? "3 stories" : "2 endpoints"}</span>
        <div onClick={() => setFull((v) => !v)} style={{ display: "flex", alignItems: "center", gap: 6, cursor: "pointer", font: "600 10px 'IBM Plex Sans'", color: "var(--ac)", padding: "5px 11px", border: "1px solid var(--tint-active-bd)", borderRadius: 7, background: "var(--tint-active)" }}>
          <span style={{ fontSize: 13 }}>{full ? "⤢" : "⤡"}</span>{t(full ? "review.grid" : "review.fullPage")}
        </div>
      </div>

      <div style={{ padding: "16px 18px" }}>
        {isVrt ? (
          <div style={{ display: "flex", flexDirection: "column", gap: 13 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>Storybook VRT</span>
              <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>visual regression — before / after</span>
            </div>
            <div style={grid}>
              {vrt.map((v) => (
                <div key={v.story} style={{ background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 10, overflow: "hidden" }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "9px 12px", borderBottom: "1px solid var(--bd-soft)" }}>
                    <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx2)" }}>{v.story}</span>
                    <div style={{ flex: 1 }} />
                    <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>Δ {v.diff}</span>
                    <span style={{ font: "500 9px 'IBM Plex Mono'", color: v.ok ? "var(--green)" : "var(--amber)", background: v.ok ? "var(--tint-green)" : "var(--tint-amber)", padding: "2px 6px", borderRadius: 4 }}>{v.ok ? "pass" : "diff"}</span>
                  </div>
                  <div style={{ display: "flex", gap: 1, background: "var(--bd)" }}>
                    <ShotCol label="BEFORE (main)" err={false} />
                    <ShotCol label={`AFTER (${task.branch})`} err={!v.ok} />
                  </div>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 13 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("review.apiEvidence")}</span>
              <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("review.reqResCapture")}</span>
            </div>
            <div style={grid}>
              {api.map((a) => (
                <div key={a.path} style={{ background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 10, overflow: "hidden" }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 9, padding: "9px 12px", borderBottom: "1px solid var(--bd-soft)" }}>
                    <span style={{ font: "600 9px 'IBM Plex Mono'", color: a.mcolor, border: `1px solid ${a.mcolor}`, padding: "2px 6px", borderRadius: 4 }}>{a.method}</span>
                    <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx2)" }}>{a.path}</span>
                    <div style={{ flex: 1 }} />
                    <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: a.scolor }}>{a.status}</span>
                    <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{a.ms}</span>
                  </div>
                  <div style={{ display: "flex", gap: 1, background: "var(--bd)" }}>
                    <div style={{ flex: 1, background: "var(--bg-deep)" }}>
                      <div style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", padding: "6px 11px" }}>REQUEST</div>
                      <pre style={{ margin: 0, padding: "0 12px 12px", fontFamily: "'IBM Plex Mono',monospace", fontSize: 11, lineHeight: 1.7, color: "var(--tx3)", whiteSpace: "pre-wrap" }}>{a.req}</pre>
                    </div>
                    <div style={{ flex: 1, background: "var(--bg-deep)" }}>
                      <div style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", padding: "6px 11px" }}>RESPONSE</div>
                      <pre style={{ margin: 0, padding: "0 12px 12px", fontFamily: "'IBM Plex Mono',monospace", fontSize: 11, lineHeight: 1.7, color: "#9fe0c2", whiteSpace: "pre-wrap" }}>{a.res}</pre>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function ShotCol({ label, err }: { label: string; err: boolean }) {
  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column" }}>
      <div style={{ font: "500 8.5px 'IBM Plex Mono'", color: "var(--tx-faint)", padding: "5px 9px", background: "var(--bg-panel)" }}>{label}</div>
      <div style={{ height: 120, background: err ? "linear-gradient(135deg,#2a1a20,#3e222d)" : "linear-gradient(135deg,#1a2230,#222d3e)", display: "flex", flexDirection: "column", alignItems: "flex-start", padding: 14, gap: 6 }}>
        <div style={{ height: 8, width: "55%", background: "rgba(255,255,255,.13)", borderRadius: 3 }} />
        <div style={{ height: 18, width: "80%", background: "rgba(255,255,255,.09)", borderRadius: 4 }} />
        <div style={{ height: 18, width: "80%", background: "rgba(255,255,255,.09)", borderRadius: 4 }} />
        {err && <div style={{ height: 12, width: "60%", background: "rgba(224,106,106,.5)", borderRadius: 4 }} />}
        <div style={{ height: 20, width: "40%", background: "rgba(79,157,255,.4)", borderRadius: 4, marginTop: 6 }} />
      </div>
    </div>
  );
}
