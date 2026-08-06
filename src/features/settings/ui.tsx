import { useRef } from "react";

// Shared building blocks for the Settings panels — faithful to Orchestra.dc.html.

export const cardStyle: React.CSSProperties = {
  background: "var(--bg-card)",
  border: "1px solid var(--bd)",
  borderRadius: 11,
  padding: "18px 20px",
  display: "flex",
  flexDirection: "column",
  gap: 14,
};

export function sectionTitle(title: string, desc: string) {
  return (
    <div>
      <div style={{ font: "700 19px 'IBM Plex Sans'", color: "var(--tx)", letterSpacing: "-0.3px" }}>{title}</div>
      <div style={{ font: "400 12.5px 'IBM Plex Sans'", color: "var(--tx-dim)", marginTop: 5, lineHeight: 1.5 }}>{desc}</div>
    </div>
  );
}

export function segStyle(active: boolean): React.CSSProperties {
  return {
    flex: 1,
    padding: "11px 12px",
    borderRadius: 9,
    cursor: "pointer",
    background: active ? "var(--tint-active)" : "var(--bg-card2)",
    border: `1px solid ${active ? "var(--tint-active-bd)" : "var(--bd2)"}`,
    transition: "background .12s, border-color .12s",
  };
}

export interface SegItem {
  id: string;
  title: string;
  sub: string;
}

export function SegGroup({ items, value, onChange }: { items: SegItem[]; value: string; onChange: (id: string) => void }) {
  return (
    <div style={{ display: "flex", gap: 8 }}>
      {items.map((it) => (
        <div key={it.id} onClick={() => onChange(it.id)} style={segStyle(value === it.id)}>
          <div style={{ font: "600 11.5px 'IBM Plex Sans'", color: "var(--tx2)" }}>{it.title}</div>
          <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)", marginTop: 3 }}>{it.sub}</div>
        </div>
      ))}
    </div>
  );
}

export function Toggle({ on, onClick, marginLeft }: { on: boolean; onClick: () => void; marginLeft?: string }) {
  return (
    <div
      onClick={onClick}
      style={{
        width: 38,
        height: 22,
        flex: "none",
        borderRadius: 11,
        position: "relative",
        cursor: "pointer",
        marginLeft,
        background: on ? "var(--ac)" : "var(--bg-card2)",
        border: `1px solid ${on ? "var(--ac)" : "var(--bd2)"}`,
        transition: "background .15s, border-color .15s",
      }}
    >
      <div
        style={{
          position: "absolute",
          top: 2,
          left: on ? 18 : 2,
          width: 16,
          height: 16,
          borderRadius: "50%",
          background: on ? "#06121e" : "var(--tx-dim)",
          transition: "left .15s",
        }}
      />
    </div>
  );
}

export function Slider({ value, min, max, onChange }: { value: number; min: number; max: number; onChange: (v: number) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const pct = ((value - min) / (max - min)) * 100;
  const apply = (clientX: number) => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const p = Math.min(1, Math.max(0, (clientX - r.left) / r.width));
    onChange(Math.round(min + p * (max - min)));
  };
  return (
    <div
      ref={ref}
      onPointerDown={(e) => {
        apply(e.clientX);
        const mv = (ev: PointerEvent) => apply(ev.clientX);
        const up = () => {
          window.removeEventListener("pointermove", mv);
          window.removeEventListener("pointerup", up);
        };
        window.addEventListener("pointermove", mv);
        window.addEventListener("pointerup", up);
      }}
      style={{ flex: 1, height: 6, background: "var(--bd3)", borderRadius: 3, position: "relative", cursor: "pointer", touchAction: "none" }}
    >
      <div style={{ position: "absolute", left: 0, top: 0, height: 6, width: `${pct}%`, borderRadius: 3, background: "linear-gradient(90deg,#34d3e0,#4f9dff)" }} />
      <div style={{ position: "absolute", top: -4, left: `calc(${pct}% - 7px)`, width: 14, height: 14, borderRadius: "50%", background: "var(--tx)", border: "2px solid var(--bg-card)", boxShadow: "0 1px 4px rgba(0,0,0,.4)" }} />
    </div>
  );
}
