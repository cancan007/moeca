import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useStore } from "@/store/useStore";
import { soloSystem, type SoloAgent } from "@/lib/templates";
import { schedules as schedulesApi, scheduleTask } from "@/lib/schedules";
import { compileRef, buildRunSpec, type TemplateStores } from "@/lib/agentTemplates";

// resolve the solos (granularities) a template ref is built from.
export function involvedSolos(templateRef: string, st: TemplateStores): SoloAgent[] {
  const [kind, id] = (templateRef ?? "").split(":");
  if (kind === "solo") {
    const s = st.solos.find((x) => x.id === id);
    return s ? [s] : [];
  }
  if (kind === "static") {
    const t = st.staticTpls.find((x) => x.id === id);
    if (!t) return [];
    const ids = t.pattern === "graph" ? t.nodes.map((n) => n.soloId).filter((x): x is string => !!x) : [t.supervisor, ...t.workers];
    const seen = new Set<string>();
    const out: SoloAgent[] = [];
    for (const sid of ids) {
      if (seen.has(sid)) continue;
      seen.add(sid);
      const s = st.solos.find((x) => x.id === sid);
      if (s) out.push(s);
    }
    return out;
  }
  return [];
}

// RunOptimizer — edit the per-granularity system prompt(s) of the template a run
// used, save to the template store, and re-sync every schedule bound to that
// template (manual runs re-compile from the store on their next launch, so no
// per-run sync is needed for them).
export function RunOptimizer({
  templateRef, templateLabel, onClose, onSynced,
}: {
  templateRef: string;
  templateLabel: string;
  onClose: () => void;
  onSynced?: () => void;
}) {
  const { t } = useTranslation();
  const solos = useStore((s) => s.solos);
  const staticTpls = useStore((s) => s.staticTpls);
  const providers = useStore((s) => s.providers);
  const tools = useStore((s) => s.tools);
  const upsertSolo = useStore((s) => s.upsertSolo);
  const st: TemplateStores = { solos, staticTpls, providers, tools };

  const involved = involvedSolos(templateRef, st);
  const [edits, setEdits] = useState<Record<string, string>>(() =>
    Object.fromEntries(involved.map((s) => [s.id, s.system ?? soloSystem(s)])),
  );
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const save = async () => {
    setSaving(true);
    setErr(null);
    try {
      // 1. write edited personas back to the template store (source of truth)
      const newSolos = solos.map((s) => (edits[s.id] !== undefined ? { ...s, system: edits[s.id] } : s));
      involved.forEach((s) => upsertSolo({ ...s, system: edits[s.id] }));
      // 2. re-sync every schedule bound to the same template
      const stores: TemplateStores = { ...st, solos: newSolos };
      const all = await schedulesApi.list().catch(() => []);
      for (const sc of all.filter((s) => s.templateRef === templateRef)) {
        // Same composition the editor uses, so re-syncing a schedule after a
        // prompt edit cannot quietly drop its milestones from the task.
        const task = scheduleTask(sc);
        const c = compileRef(sc.templateRef, stores, task);
        await schedulesApi.update(sc.id, {
          name: sc.name, cron: sc.cron, perspective: sc.perspective,
          active: sc.active, goal: sc.goal, milestones: sc.milestones,
          templateLabel: c?.label ?? sc.templateLabel, templateRef: sc.templateRef,
          runSpec: c ? buildRunSpec(c.stages, { unattended: true }) : sc.runSpec,
        });
      }
      onSynced?.();
      onClose();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(6,8,11,.6)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 55 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: "62%", minWidth: 620, maxHeight: "82%", background: "var(--bg-panel)", border: "1px solid var(--bd)", borderRadius: 12, display: "flex", flexDirection: "column" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "14px 18px", borderBottom: "1px solid var(--bd)" }}>
          <span style={{ font: "700 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("runs.optimizeTitle")}</span>
          <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--ac)" }}>{templateLabel}</span>
          <div style={{ flex: 1 }} />
          <div onClick={onClose} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 18px 'IBM Plex Sans'", padding: "0 4px" }}>✕</div>
        </div>
        <div style={{ flex: 1, minHeight: 0, overflowY: "auto", padding: "16px 18px", display: "flex", flexDirection: "column", gap: 16 }}>
          {involved.length === 0 && <div style={{ font: "400 12px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("runs.noEditableGranularity")}</div>}
          {involved.map((s) => (
            <div key={s.id} style={{ display: "flex", flexDirection: "column", gap: 7 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                <div style={{ width: 8, height: 8, borderRadius: 2, background: s.dot }} />
                <span style={{ font: "600 12px 'IBM Plex Sans'", color: "var(--tx2)" }}>{s.name}</span>
                <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{s.role} · {s.model}</span>
              </div>
              <textarea
                value={edits[s.id] ?? ""}
                onChange={(e) => setEdits((m) => ({ ...m, [s.id]: e.target.value }))}
                rows={6}
                style={{ background: "var(--bg-card2)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "10px 12px", font: "400 12px/1.6 'IBM Plex Mono'", color: "var(--tx)", outline: "none", resize: "vertical" }}
              />
            </div>
          ))}
          {err && <div style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</div>}
        </div>
        <div style={{ display: "flex", gap: 8, padding: "12px 18px", borderTop: "1px solid var(--bd)" }}>
          <div onClick={() => !saving && involved.length > 0 && save()} style={{ font: "600 11.5px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 16px", borderRadius: 8, cursor: saving ? "default" : "pointer", opacity: saving ? 0.6 : 1 }}>
            {saving ? t("runs.savingSync") : t("runs.saveToTemplate")}
          </div>
          <div onClick={onClose} style={{ font: "500 11.5px 'IBM Plex Sans'", color: "var(--tx3)", padding: "8px 16px", cursor: "pointer" }}>{t("common.cancel")}</div>
        </div>
      </div>
    </div>
  );
}
