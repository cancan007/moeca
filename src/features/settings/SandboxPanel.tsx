import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { cardStyle, sectionTitle, Toggle } from "./ui";
import { sandbox, type ImagePolicy } from "@/lib/sandbox";

const inputStyle: React.CSSProperties = {
  background: "var(--bg-deep)",
  border: "1px solid var(--bd2)",
  borderRadius: 7,
  padding: "8px 11px",
  font: "500 11px 'IBM Plex Mono'",
  color: "var(--tx)",
  outline: "none",
};

// ImagesCard manages the container-image allowlist.
//
// A stage names one of these policies; the controller supplies the image
// reference, the network posture, the resource caps and the scratch mounts. The
// hardening flags are the same for every image, so adding one here is a
// supply-chain decision, not an isolation one — which is why the only thing this
// card can grant is *which* image, never *how* it runs.
//
// The unattended toggle is the second axis, and the reason promotion is a
// separate act: Delivery runs are attended (a reviewer is in the drawer) and may
// use anything, while a Daily schedule fires with nobody watching and is limited
// to images someone deliberately approved for it.
function ImagesCard() {
  const { t } = useTranslation();
  const [images, setImages] = useState<ImagePolicy[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [name, setName] = useState("");
  const [ref, setRef] = useState("");
  const [desc, setDesc] = useState("");
  const [noNetwork, setNoNetwork] = useState(false);

  const load = () =>
    sandbox
      .images()
      .then((r) => setImages(r.images))
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));

  useEffect(() => {
    load();
  }, []);

  const apply = async (fn: () => Promise<{ images: ImagePolicy[] }>) => {
    setBusy(true);
    setErr(null);
    try {
      setImages((await fn()).images);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const add = () => {
    if (!name.trim() || !ref.trim()) return;
    apply(async () => {
      const res = await sandbox.saveImage({
        name: name.trim(),
        ref: ref.trim(),
        description: desc.trim(),
        network: noNetwork ? "none" : "egress",
      });
      setName("");
      setRef("");
      setDesc("");
      setNoNetwork(false);
      return res;
    });
  };

  const promote = (img: ImagePolicy) =>
    apply(() => sandbox.saveImage({ ...img, unattended: !img.unattended }));

  const remove = (img: ImagePolicy) => apply(() => sandbox.deleteImage(img.name));

  const canAdd = name.trim() !== "" && ref.trim() !== "" && !busy;

  return (
    <div style={{ ...cardStyle, gap: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.sandbox.images.title")}</span>
        <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>allowlist</span>
        <span style={{ marginLeft: "auto", font: "500 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
          {images ? t("settings.sandbox.countUnit", { count: images.length }) : "—"}
        </span>
      </div>
      <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>
        <Trans i18nKey="settings.sandbox.images.note" components={{ b: <strong style={{ color: "var(--tx3)" }} /> }} />
      </span>

      <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
        {(images ?? []).map((img) => (
          <div
            key={img.name}
            style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "9px 12px", flexWrap: "wrap" }}
          >
            <span style={{ font: "600 11px 'IBM Plex Mono'", color: "var(--tx2)", minWidth: 62 }}>{img.name}</span>
            <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--tx-dim)", flex: 1, minWidth: 180 }}>{img.ref}</span>
            {img.network === "none" && (
              <span style={{ font: "600 9px 'IBM Plex Mono'", color: "#d39a4e", background: "var(--tint-amber)", border: "1px solid var(--bd2)", padding: "2px 7px", borderRadius: 4 }}>
                network none
              </span>
            )}
            <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 7px", borderRadius: 4 }}>
              {img.custom ? "custom" : "built-in"}
            </span>
            <span
              title={t("settings.sandbox.images.unattendedTip")}
              style={{ font: "500 9.5px 'IBM Plex Mono'", color: img.unattended ? "#67c9a4" : "var(--tx-faint)" }}
            >
              {t("settings.sandbox.images.unattendedBadge")}
            </span>
            {img.custom ? (
              <Toggle on={!!img.unattended} onClick={() => !busy && promote(img)} />
            ) : (
              <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", width: 38, textAlign: "center" }}>
                {img.unattended ? "✓" : "—"}
              </span>
            )}
            {img.custom && (
              <div onClick={() => !busy && remove(img)} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 15px 'IBM Plex Sans'", padding: "0 4px", lineHeight: 1 }}>
                ✕
              </div>
            )}
            {img.description && (
              <div style={{ flexBasis: "100%", font: "400 10px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{img.description}</div>
            )}
          </div>
        ))}
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: 9, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 9, padding: "12px 13px" }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("settings.sandbox.images.namePlaceholder")} spellCheck={false} style={{ ...inputStyle, width: 170 }} />
          <input value={ref} onChange={(e) => setRef(e.target.value)} placeholder={t("settings.sandbox.images.refPlaceholder")} spellCheck={false} style={{ ...inputStyle, flex: 1, minWidth: 220 }} />
          <div onClick={() => canAdd && add()} style={{ font: "600 11px 'IBM Plex Sans'", color: canAdd ? "#06121e" : "var(--tx-faint)", background: canAdd ? "var(--ac)" : "var(--bg-card2)", border: canAdd ? "none" : "1px solid var(--bd2)", padding: "8px 14px", borderRadius: 7, cursor: canAdd ? "pointer" : "not-allowed" }}>
            {t("common.add")}
          </div>
        </div>
        <input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder={t("settings.sandbox.images.descPlaceholder")} spellCheck={false} style={{ ...inputStyle, width: "100%", font: "400 11px 'IBM Plex Sans'" }} />
        <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
          <Toggle on={noNetwork} onClick={() => setNoNetwork((v) => !v)} />
          <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>
            <Trans i18nKey="settings.sandbox.images.noNetwork" components={{ code: <span style={{ font: "500 10px 'IBM Plex Mono'" }} /> }} />
          </span>
        </div>
        <div style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", lineHeight: 1.6 }}>
          {t("settings.sandbox.images.promoteNote")}
        </div>
      </div>

      {err && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</span>}
    </div>
  );
}

// RetentionCard controls how long finished runs' archives are kept: their stage
// logs and the commits describing what each stage produced. Nothing else deletes
// them, so this is the only bound on that directory's growth.
function RetentionCard() {
  const { t } = useTranslation();
  const [days, setDays] = useState<number | null>(null);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    sandbox
      .retention()
      .then((r) => { setDays(r.days); setDraft(String(r.days)); })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  const save = async () => {
    const n = Number(draft);
    if (!Number.isInteger(n) || n < 0) {
      setErr(t("settings.sandbox.retention.invalid"));
      return;
    }
    setBusy(true); setErr(null); setSaved(false);
    try {
      const r = await sandbox.setRetention(n);
      setDays(r.days); setDraft(String(r.days)); setSaved(true);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  const dirty = days !== null && draft !== String(days);

  return (
    <div style={{ ...cardStyle, gap: 12 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
        <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.sandbox.retention.title")}</span>
        <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{t("settings.sandbox.retention.subtitle")}</span>
      </div>
      <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>
        <Trans i18nKey="settings.sandbox.retention.desc" components={{ b: <strong style={{ color: "var(--tx3)" }} /> }} />
      </span>
      <div style={{ display: "flex", alignItems: "center", gap: 9 }}>
        <input
          value={draft}
          onChange={(e) => { setDraft(e.target.value); setSaved(false); }}
          onKeyDown={(e) => e.key === "Enter" && dirty && !busy && save()}
          inputMode="numeric"
          disabled={days === null}
          style={{ width: 90, background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "500 12px 'IBM Plex Mono'", color: "var(--tx)", outline: "none" }}
        />
        <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{t("settings.sandbox.retention.days")}</span>
        <div
          onClick={() => dirty && !busy && save()}
          style={{ font: "600 11px 'IBM Plex Sans'", color: dirty ? "#06121e" : "var(--tx-faint)", background: dirty ? "var(--ac)" : "var(--bg-card2)", border: dirty ? "none" : "1px solid var(--bd2)", padding: "8px 14px", borderRadius: 7, cursor: dirty && !busy ? "pointer" : "not-allowed" }}
        >
          {busy ? t("settings.sandbox.retention.saving") : t("common.save")}
        </div>
        {saved && !dirty && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "#67c9a4" }}>{t("settings.sandbox.retention.saved")}</span>}
        {days === 0 && !dirty && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "#d39a4e" }}>{t("settings.sandbox.retention.forever")}</span>}
      </div>
      {err && <span style={{ font: "400 10px 'IBM Plex Mono'", color: "var(--red)" }}>{err}</span>}
    </div>
  );
}

type Level = "block" | "ask" | "warn";
type Scope = "command" | "path" | "network";

interface Policy {
  id: number;
  pattern: string;
  scope: Scope;
  level: Level;
  /** i18n key under settings.sandbox.policyNotes for the shipped examples;
   *  policies the user types in carry no note. */
  noteKey?: string;
}

const levelColor: Record<Level, { fg: string; bg: string; bd: string }> = {
  block: { fg: "#e06a6a", bg: "var(--tint-red)", bd: "var(--tint-red-bd)" },
  ask: { fg: "#d39a4e", bg: "var(--tint-amber)", bd: "var(--bd2)" },
  warn: { fg: "#5b9fe8", bg: "var(--tint-blue)", bd: "var(--tint-blue-bd)" },
};
const nextLevel: Record<Level, Level> = { block: "ask", ask: "warn", warn: "block" };

// The shipped example policies. `noteKey` rather than a note: the pattern is a
// literal and stays put, while the human gloss beside it follows the UI.
const initialPolicies: Policy[] = [
  { id: 1, pattern: "rm -rf /", scope: "command", level: "block", noteKey: "destructiveDelete" },
  { id: 2, pattern: "aws s3 rm", scope: "command", level: "block", noteKey: "cloudAssetDelete" },
  { id: 3, pattern: "*.pem", scope: "path", level: "block", noteKey: "privateKeyRead" },
  { id: 4, pattern: "10.0.0.0/8", scope: "network", level: "block", noteKey: "internalNetwork" },
  { id: 5, pattern: "git push --force", scope: "command", level: "ask", noteKey: "historyRewrite" },
  { id: 6, pattern: "curl *.internal", scope: "network", level: "warn", noteKey: "internalEndpoint" },
];

const toggleKeys = ["env", "path", "net", "registry"];

export function SandboxPanel() {
  const { t } = useTranslation();
  const [toggles, setToggles] = useState<Record<string, boolean>>({ env: true, path: true, net: true, registry: true });
  const [policies, setPolicies] = useState<Policy[]>(initialPolicies);

  const scopeLabel = (s: Scope) => t(`settings.sandbox.scopes.${s}`);
  const toggleRows = toggleKeys.map((key) => ({ key, title: t(`settings.sandbox.toggles.${key}.title`), sub: t(`settings.sandbox.toggles.${key}.sub`) }));
  const [draft, setDraft] = useState("");
  const [scope, setScope] = useState<Scope>("command");
  const [level, setLevel] = useState<Level>("block");

  const addPolicy = () => {
    const p = draft.trim();
    if (!p) return;
    setPolicies((list) => [...list, { id: Date.now(), pattern: p, scope, level }]);
    setDraft("");
  };
  const cycle = (id: number) => setPolicies((list) => list.map((p) => (p.id === id ? { ...p, level: nextLevel[p.level] } : p)));
  const remove = (id: number) => setPolicies((list) => list.filter((p) => p.id !== id));

  const scopeBtn = (s: Scope): React.CSSProperties => ({ font: "500 9.5px 'IBM Plex Mono'", color: scope === s ? "var(--tx)" : "var(--tx-dim)", background: scope === s ? "var(--tint-active)" : "var(--bg-card)", border: `1px solid ${scope === s ? "var(--tint-active-bd)" : "var(--bd2)"}`, padding: "3px 9px", borderRadius: 6, cursor: "pointer" });
  const levelBtn = (l: Level): React.CSSProperties => ({ font: "500 9.5px 'IBM Plex Mono'", color: level === l ? levelColor[l].fg : "var(--tx-dim)", background: level === l ? levelColor[l].bg : "var(--bg-card)", border: `1px solid ${level === l ? levelColor[l].bd : "var(--bd2)"}`, padding: "3px 9px", borderRadius: 6, cursor: "pointer" });

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 22 }}>
      {sectionTitle(t("settings.sandbox.title"), t("settings.sandbox.desc"))}

      <div style={{ display: "flex", alignItems: "center", gap: 12, background: "var(--tint-green)", border: "1px solid var(--tint-green-bd)", borderRadius: 11, padding: "14px 18px" }}>
        <div style={{ width: 9, height: 9, borderRadius: "50%", background: "#3fbf8f", boxShadow: "0 0 8px #3fbf8f" }} />
        <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "#9fe0c2" }}>{t("settings.sandbox.healthy")}</span>
        <span style={{ font: "400 11px 'IBM Plex Mono'", color: "#5a8f6f" }}>acme/web-app · container #a4f2</span>
        <span style={{ marginLeft: "auto", font: "500 10px 'IBM Plex Mono'", color: "#67c9a4" }}>uptime 4h 12m</span>
      </div>

      <ImagesCard />

      <RetentionCard />

      <div style={{ background: "var(--bg-card)", border: "1px solid var(--bd)", borderRadius: 11, padding: "6px 20px" }}>
        {toggleRows.map((r, i) => (
          <div key={r.key} style={{ display: "flex", alignItems: "center", gap: 12, padding: "15px 0", borderBottom: i < toggleRows.length - 1 ? "1px solid var(--bd-soft)" : "none" }}>
            <div style={{ display: "flex", flexDirection: "column", gap: 3, flex: 1 }}>
              <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{r.title}</span>
              <span style={{ font: "400 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{r.sub}</span>
            </div>
            <Toggle on={toggles[r.key]} onClick={() => setToggles((t) => ({ ...t, [r.key]: !t[r.key] }))} />
          </div>
        ))}
      </div>

      {/* allowlist */}
      <div style={{ ...cardStyle, gap: 12 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.sandbox.writablePaths")}</span>
          <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>allowlist</span>
          <div style={{ marginLeft: "auto", font: "500 11px 'IBM Plex Mono'", color: "var(--ac)", cursor: "pointer" }}>+ {t("common.add")}</div>
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
          {([
            { path: "/workspace/acme-web-app/**", mode: "rw", color: "#67c9a4", bg: "var(--tint-green)", stroke: "#67c9a4" },
            { path: "/workspace/.cache/**", mode: "ro", color: "#5b9fe8", bg: "var(--tint-blue)", stroke: "var(--tx-dim)" },
          ] as const).map((row) => (
            <div key={row.path} style={{ display: "flex", alignItems: "center", gap: 9, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "9px 11px" }}>
              <svg width="13" height="13" viewBox="0 0 14 14" fill="none" stroke={row.stroke} strokeWidth="1.5"><path d="M2 4.5h4l1 1.5h5v5.5H2z" /></svg>
              <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx3)", flex: 1 }}>{row.path}</span>
              <span style={{ font: "500 9px 'IBM Plex Mono'", color: row.color, background: row.bg, padding: "2px 6px", borderRadius: 4 }}>{row.mode}</span>
            </div>
          ))}
        </div>
      </div>

      {/* forbidden commands */}
      <div style={{ ...cardStyle, gap: 12 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "600 13.5px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("settings.sandbox.policies.title")}</span>
          <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>settings.json · PreToolUse hooks</span>
          <span style={{ marginLeft: "auto", font: "500 10px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.sandbox.countUnit", { count: policies.length })}</span>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 9, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 9, padding: "12px 13px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ font: "600 11px 'IBM Plex Mono'", color: "var(--ac)" }}>$</span>
            <input
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && addPolicy()}
              placeholder={t("settings.sandbox.policies.placeholder")}
              spellCheck={false}
              style={{ flex: 1, background: "var(--bg-card)", border: "1px solid var(--bd2)", borderRadius: 7, padding: "8px 11px", font: "500 11px 'IBM Plex Mono'", color: "var(--tx)", outline: "none" }}
            />
            <div onClick={addPolicy} style={{ font: "600 11px 'IBM Plex Sans'", color: "#06121e", background: "var(--ac)", padding: "8px 14px", borderRadius: 7, cursor: "pointer" }}>{t("common.add")}</div>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 7, flexWrap: "wrap" }}>
            <span style={{ font: "500 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px" }}>{t("settings.sandbox.policies.scopeLabel")}</span>
            {(["command", "path", "network"] as Scope[]).map((s) => (
              <div key={s} onClick={() => setScope(s)} style={scopeBtn(s)}>{scopeLabel(s)}</div>
            ))}
            <div style={{ width: 1, height: 16, background: "var(--bd-sep)", margin: "0 3px" }} />
            <span style={{ font: "500 9px 'IBM Plex Mono'", color: "var(--tx-faint)", letterSpacing: "0.4px" }}>{t("settings.sandbox.policies.levelLabel")}</span>
            {(["block", "ask", "warn"] as Level[]).map((l) => (
              <div key={l} onClick={() => setLevel(l)} style={levelBtn(l)}>{l}</div>
            ))}
          </div>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 7 }}>
          {policies.map((p) => {
            const c = levelColor[p.level];
            return (
              <div key={p.id} style={{ display: "flex", alignItems: "center", gap: 10, background: "var(--bg-inset2)", border: "1px solid var(--bd3)", borderRadius: 7, padding: "9px 12px" }}>
                <div onClick={() => cycle(p.id)} style={{ font: "600 9px 'IBM Plex Mono'", color: c.fg, background: c.bg, border: `1px solid ${c.bd}`, padding: "3px 8px", borderRadius: 5, cursor: "pointer", width: 44, textAlign: "center" }}>{p.level}</div>
                <span style={{ font: "500 11px 'IBM Plex Mono'", color: "var(--tx2)" }}>{p.pattern}</span>
                <span style={{ font: "400 9px 'IBM Plex Mono'", color: "var(--tx-faint)", background: "var(--bg-card2)", border: "1px solid var(--bd2)", padding: "2px 7px", borderRadius: 4 }}>{scopeLabel(p.scope)}</span>
                {p.noteKey && <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-dim)" }}>{t(`settings.sandbox.policyNotes.${p.noteKey}`)}</span>}
                <div style={{ flex: 1 }} />
                <div onClick={() => remove(p.id)} style={{ cursor: "pointer", color: "var(--tx-mut)", font: "400 15px 'IBM Plex Sans'", padding: "0 4px", lineHeight: 1 }}>✕</div>
              </div>
            );
          })}
        </div>

        <div style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("settings.sandbox.policies.footnote")}</div>
      </div>
    </div>
  );
}
