import { useTranslation } from "react-i18next";
import type { DeliveryTask, CI } from "@/store/useStore";
import { useStore } from "@/store/useStore";

function GitIcon({ size = 11 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 14 14" fill="none" stroke="var(--tx-faint)" strokeWidth="1.5">
      <path d="M4 2v6a2 2 0 0 0 2 2h4M4 2a1.5 1.5 0 1 1 0 .01M10 10a1.5 1.5 0 1 1 0 .01M4 8V4" />
    </svg>
  );
}

export function CIBadge({ ci }: { ci: CI }) {
  const { t } = useTranslation();
  if (ci === "none") return null;
  if (ci === "passed")
    return (
      <span style={{ display: "flex", alignItems: "center", gap: 4, font: "500 8.5px 'IBM Plex Mono'", color: "var(--green)", background: "var(--tint-green)", border: "1px solid var(--tint-green-bd)", padding: "2px 6px", borderRadius: 5 }}>
        <svg width="9" height="9" viewBox="0 0 14 14" fill="none" stroke="var(--green)" strokeWidth="2.4"><path d="M2.5 7.5l3 3 6-7" /></svg>
        CI
      </span>
    );
  if (ci === "running")
    return (
      <span style={{ display: "flex", alignItems: "center", gap: 4, font: "500 8.5px 'IBM Plex Mono'", color: "var(--ac)", background: "var(--tint-active)", border: "1px solid var(--tint-active-bd)", padding: "2px 6px", borderRadius: 5 }}>
        <span className="oc-spin" style={{ width: 8, height: 8, borderRadius: "50%", border: "1.4px solid var(--ac)", borderTopColor: "transparent", display: "block" }} />
        CI
      </span>
    );
  return (
    <span style={{ display: "flex", alignItems: "center", gap: 4, font: "500 8.5px 'IBM Plex Mono'", color: "var(--red)", background: "var(--tint-red)", border: "1px solid var(--tint-red-bd)", padding: "2px 6px", borderRadius: 5 }}>
      <svg width="9" height="9" viewBox="0 0 14 14" fill="none" stroke="var(--red)" strokeWidth="2"><path d="M4 4l6 6M10 4l-6 6" /></svg>
      {t("common.failed")}
    </span>
  );
}

function Avatar({ gradient }: { gradient: boolean }) {
  return (
    <div style={{ width: 16, height: 16, borderRadius: "50%", background: gradient ? "linear-gradient(135deg,#4f9dff,#34d3e0)" : "var(--avatar-mut)" }} />
  );
}

export function DeliveryCard({ task }: { task: DeliveryTask }) {
  const openReview = useStore((s) => s.openReview);

  if (task.status === "done") {
    return (
      <div
        draggable
        onDragStart={(e) => e.dataTransfer.setData("text/plain", task.id)}
        style={{ background: "var(--bg-done)", border: "1px solid var(--bd-done)", borderRadius: 9, padding: "12px 13px", display: "flex", flexDirection: "column", gap: 8, opacity: 0.85, cursor: "grab" }}
      >
        <div style={{ display: "flex", alignItems: "center" }}>
          <div style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx-done)", lineHeight: 1.4 }}>{task.title}</div>
          <span style={{ marginLeft: "auto" }}>
            <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke="var(--green)" strokeWidth="2"><path d="M2.5 7.5l3 3 6-7" /></svg>
          </span>
        </div>
        <div style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{task.merged}</div>
      </div>
    );
  }

  const isWorking = task.status === "working";

  return (
    <div
      draggable
      onDragStart={(e) => e.dataTransfer.setData("text/plain", task.id)}
      onClick={() => openReview(task.id)}
      style={{
        background: "var(--bg-card2)",
        border: `1px solid ${isWorking ? "var(--tint-active-bd)" : "var(--bd2)"}`,
        boxShadow: isWorking ? "0 0 0 1px var(--tint-active-bd)" : "none",
        borderRadius: 9,
        padding: "12px 13px",
        display: "flex",
        flexDirection: "column",
        gap: 9,
        cursor: "pointer",
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
        <span style={{ font: "600 9px 'IBM Plex Mono'", color: "var(--tx3)", background: "var(--bg-thumb)", border: "1px solid var(--bd2)", padding: "2px 6px", borderRadius: 4 }}>{task.id}</span>
        {isWorking && task.review && (
          <span style={{ font: "500 9px 'IBM Plex Mono'", color: "var(--ac)", background: "var(--tint-accent)", padding: "2px 6px", borderRadius: 5 }}>review →</span>
        )}
        <div style={{ flex: 1 }} />
        <CIBadge ci={task.ci} />
      </div>

      <div style={{ font: "600 13px 'IBM Plex Sans'", color: "var(--tx)", lineHeight: 1.4 }}>{task.title}</div>

      <div style={{ display: "flex", alignItems: "center", gap: 6, font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
        <span>{task.branch}</span>
        <span style={{ color: "var(--ac)" }}>→</span>
        <span style={{ color: "#67c9a4" }}>{task.target}</span>
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
        <GitIcon />
        <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{task.worktree}</span>
        <div style={{ flex: 1 }} />
        {task.active && (
          <>
            <span className="oc-active-dot" style={{ width: 6, height: 6, borderRadius: "50%", background: "var(--green)" }} />
            <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "#67c9a4" }}>active</span>
          </>
        )}
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: 5 }}>
        <span style={{ display: "flex", alignItems: "center", gap: 5, font: "500 9px 'IBM Plex Mono'", color: "var(--tx-mut)", background: "var(--bg-card)", border: "1px solid var(--bd2)", padding: "2px 7px", borderRadius: 5 }}>
          <span style={{ width: 6, height: 6, borderRadius: "50%", background: task.pipeline.color }} />
          {task.pipeline.name}
        </span>
      </div>

      <div style={{ display: "flex", alignItems: "center", gap: 9, paddingTop: 7, borderTop: "1px solid var(--bd3)" }}>
        <Avatar gradient={task.agentGradient} />
        <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-mut)" }}>{task.agent}</span>
        <span style={{ marginLeft: "auto", font: "600 10px 'IBM Plex Mono'", color: "var(--green)" }}>{task.add}</span>
        <span style={{ font: "600 10px 'IBM Plex Mono'", color: "var(--red)" }}>{task.del}</span>
        <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{task.files}f</span>
      </div>
    </div>
  );
}
