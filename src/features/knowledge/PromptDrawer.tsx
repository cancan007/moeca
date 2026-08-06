import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { archExample, type SoloAgent } from "@/lib/templates";
import { Notice, railHeading } from "./ui";

// Editing a stage's system prompt without leaving the trace.
//
// The point of putting it here is that the evidence and the fix are the same
// screen: seeing that a stage reached nothing useful is what tells you the
// prompt is wrong, and walking away to another screen to change it loses the
// thing you just learned.
//
// Two facts have to survive that convenience, and both are stated in the UI
// rather than assumed:
//
//   The prompt belongs to the agent template, not to this task. Saving changes
//   every future run of every task using that template. It looks like editing
//   what you are looking at; it is editing a shared setting.
//
//   The trace behind this drawer is a past execution. Editing cannot change it.
//   Past and future sit side by side here, and nothing on the canvas will move
//   when this is saved.

export function PromptDrawer({
  agent,
  stageName,
  usedByTasks,
  onSave,
  onClose,
}: {
  agent?: SoloAgent;
  stageName: string;
  /** how many templates reference this agent, so the blast radius is visible. */
  usedByTasks: number;
  onSave: (system: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  // Falling back to the archetype example matches what the runtime does when
  // `system` is unset, so the editor opens on the prompt that actually ran
  // rather than on a blank box.
  const effective = agent?.system ?? (agent ? archExample(agent.arch) : "");
  const [draft, setDraft] = useState(effective);

  // Switching stages must reload the editor, or an edit would silently follow
  // the user onto another agent's prompt.
  useEffect(() => setDraft(effective), [agent?.id, effective]);

  const dirty = draft !== effective;

  return (
    <div
      style={{
        position: "absolute",
        right: 0,
        top: 0,
        bottom: 0,
        width: 352,
        zIndex: 20,
        background: "var(--bg-panel)",
        borderLeft: "1px solid var(--bd2)",
        boxShadow: "-14px 0 34px rgba(0,0,0,.34)",
        display: "flex",
        flexDirection: "column",
      }}
    >
      <div
        style={{
          padding: "12px 14px",
          borderBottom: "1px solid var(--bd-soft)",
          display: "flex",
          alignItems: "center",
          gap: 8,
        }}
      >
        <div
          style={{
            width: 7,
            height: 7,
            borderRadius: 2,
            background: agent ? "var(--ac)" : "var(--tx-faint)",
            flex: "none",
          }}
        />
        <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 2 }}>
          <span style={{ font: "600 12.5px 'IBM Plex Sans'", color: "var(--tx)" }}>
            {t("knowledge.stageSystemPrompt", { stage: stageName })}
          </span>
          <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)" }}>
            {agent ? `${agent.name} · ${agent.model}` : t("knowledge.agentUnknown")}
          </span>
        </div>
        <div
          onClick={onClose}
          style={{
            cursor: "pointer",
            color: "var(--tx-mut)",
            font: "400 16px 'IBM Plex Sans'",
            padding: "0 2px",
          }}
        >
          ✕
        </div>
      </div>

      <div
        style={{
          padding: "11px 14px",
          display: "flex",
          flexDirection: "column",
          gap: 9,
          overflowY: "auto",
          flex: 1,
        }}
      >
        {!agent ? (
          <Notice tone="warn">
            {t("knowledge.noTemplateForStage")}
          </Notice>
        ) : (
          <>
            <Notice tone="warn">
              <span style={{ font: "700 11px 'IBM Plex Mono'", flex: "none" }}>!</span>
              <span>
                <Trans i18nKey="knowledge.promptScope" values={{ name: agent.name }} components={{ b: <b /> }} />
              </span>
            </Notice>
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              spellCheck={false}
              style={{
                height: 250,
                resize: "vertical",
                background: "var(--bg-deep)",
                border: "1px solid var(--bd2)",
                borderRadius: 9,
                padding: "11px 12px",
                fontFamily: "'IBM Plex Mono', monospace",
                fontSize: 11.5,
                lineHeight: 1.7,
                color: "var(--tx2)",
                outline: "none",
              }}
            />
            <Notice tone="info">
              <Trans i18nKey="knowledge.promptFutureOnly" components={{ b: <b /> }} />
            </Notice>
          </>
        )}
      </div>

      {agent ? (
        <div
          style={{
            padding: "11px 14px",
            borderTop: "1px solid var(--bd-soft)",
            display: "flex",
            flexDirection: "column",
            gap: 8,
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 8,
              background: "var(--bg-card)",
              border: "1px solid var(--bd2)",
              borderRadius: 7,
              padding: "8px 10px",
            }}
          >
            <span style={{ ...railHeading, flex: 1 }}>{t("knowledge.blastRadius")}</span>
            <span style={{ font: "600 11px 'IBM Plex Mono'", color: "var(--amber)" }}>
              {t("knowledge.templateCount", { count: usedByTasks })}
            </span>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", flex: 1 }}>
              {t(dirty ? "settings.agents.unsaved" : "knowledge.noChanges")}
            </span>
            <div
              onClick={dirty ? () => setDraft(effective) : undefined}
              style={{
                font: "500 10.5px 'IBM Plex Sans'",
                color: dirty ? "var(--tx3)" : "var(--tx-dim)",
                border: "1px solid var(--bd2)",
                padding: "6px 12px",
                borderRadius: 7,
                cursor: dirty ? "pointer" : "default",
              }}
            >
              {t("review.revert")}
            </div>
            <div
              onClick={dirty ? () => onSave(draft) : undefined}
              style={{
                font: "600 10.5px 'IBM Plex Sans'",
                color: dirty ? "var(--bg-deep)" : "var(--tx-dim)",
                background: dirty ? "var(--ac)" : "transparent",
                border: `1px solid ${dirty ? "var(--ac)" : "var(--bd2)"}`,
                padding: "6px 14px",
                borderRadius: 7,
                cursor: dirty ? "pointer" : "default",
              }}
            >
              {t("common.save")}
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}
