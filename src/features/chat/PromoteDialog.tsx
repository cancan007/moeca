import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { delivery, type RepoInfo } from "@/lib/delivery";

/**
 * Turning an answer into a Delivery task.
 *
 * A conversation is deliberately not git work — it writes into a plain directory
 * and produces no branch. So "raise this as a task" cannot be a one-click action
 * hidden behind a button: it has to say which repository, on which branch, because
 * those are the two facts a chat does not have and a task cannot exist without.
 *
 * What carries over is the answer itself, as the task's goal. The conversation
 * stays where it is; this is a hand-off, not a move.
 */
export function PromoteDialog({
  text,
  title,
  onClose,
}: {
  text: string;
  title: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [repos, setRepos] = useState<RepoInfo[]>([]);
  const [repo, setRepo] = useState("");
  const [branch, setBranch] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    delivery.repos()
      .then((rs) => {
        setRepos(rs);
        setRepo((r) => r || rs[0]?.name || "");
      })
      .catch((e) => setErr(e instanceof Error ? e.message : String(e)));
  }, []);

  // A branch name derived from the conversation, not from the answer: the answer
  // is prose and would make a terrible ref. Slugged so git accepts it.
  useEffect(() => {
    const slug = (title || text)
      .slice(0, 40)
      .toLowerCase()
      .replace(/[^a-z0-9぀-ヿ一-鿿]+/g, "-")
      .replace(/^-+|-+$/g, "");
    setBranch((b) => b || `chat/${slug || "task"}`);
  }, [title, text]);

  const create = async () => {
    if (!repo || !branch.trim()) return;
    setBusy(true);
    setErr(null);
    try {
      await delivery.createTask(repo, branch.trim());
      // The answer becomes the goal. setTaskMeta wants at least one milestone
      // alongside a goal, so the conversation's own title is the first one —
      // inventing a checklist nobody asked for would be worse.
      await delivery.setTaskMeta(repo, branch.trim(), text, [
        { title: title || t("chat.promote.firstMilestone"), done: false },
      ]);
      onClose();
      navigate("/delivery");
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div
      onClick={onClose}
      style={{ animation: "ocFade .16s ease-out both", position: "absolute", inset: 0, zIndex: 45, background: "rgba(4,6,9,.55)", display: "flex", alignItems: "center", justifyContent: "center", padding: 24 }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{ animation: "ocPop .2s cubic-bezier(.2,.8,.2,1) both", width: 460, maxWidth: "100%", background: "var(--bg-panel)", borderRadius: 16, boxShadow: "0 24px 70px rgba(0,0,0,.5)", display: "flex", flexDirection: "column", gap: 14, padding: "20px 22px" }}
      >
        <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          <span style={{ font: "600 14px 'IBM Plex Sans'", color: "var(--tx)" }}>{t("chat.promote.title")}</span>
          <span style={{ font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx-dim)", lineHeight: 1.6 }}>{t("chat.promote.desc")}</span>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("chat.promote.repo")}</span>
          <select
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            style={{ background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "9px 11px", font: "500 12px 'IBM Plex Sans'", color: "var(--tx)", outline: "none", colorScheme: "dark" }}
          >
            {repos.length === 0 && <option value="">{t("chat.promote.noRepos")}</option>}
            {repos.map((r) => (
              <option key={r.name} value={r.name}>{r.name}</option>
            ))}
          </select>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          <span style={{ font: "600 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>{t("chat.promote.branch")}</span>
          <input
            value={branch}
            onChange={(e) => setBranch(e.target.value)}
            style={{ background: "var(--bg-deep)", border: "1px solid var(--bd2)", borderRadius: 8, padding: "9px 11px", font: "500 12px 'IBM Plex Mono'", color: "var(--tx)", outline: "none" }}
          />
        </div>

        <div style={{ background: "var(--bg-card)", borderRadius: 10, padding: "10px 12px", maxHeight: 120, overflowY: "auto" }}>
          <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx3)", lineHeight: 1.7, whiteSpace: "pre-wrap" }}>
            {text.slice(0, 400)}{text.length > 400 ? "…" : ""}
          </span>
        </div>

        {err && <span style={{ font: "400 10.5px 'IBM Plex Mono'", color: "var(--red)", wordBreak: "break-word" }}>{err}</span>}

        <div style={{ display: "flex", alignItems: "center", gap: 8, justifyContent: "flex-end" }}>
          <div onClick={onClose} style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "7px 12px", borderRadius: 14, cursor: "pointer" }}>
            {t("chat.panel.cancel")}
          </div>
          <div
            onClick={() => void create()}
            style={{
              font: "600 11px 'IBM Plex Sans'",
              color: repo && branch.trim() ? "#06121e" : "var(--tx-faint)",
              background: repo && branch.trim() ? "var(--ac)" : "var(--bg-card2)",
              padding: "7px 14px", borderRadius: 14,
              cursor: repo && branch.trim() ? "pointer" : "not-allowed",
            }}
          >
            {busy ? t("chat.promote.creating") : t("chat.promote.create")}
          </div>
        </div>
      </div>
    </div>
  );
}
