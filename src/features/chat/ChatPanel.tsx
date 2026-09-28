import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { chat } from "@/lib/chat";
import { formatSize } from "@/features/daily/ArtifactGallery";
import type { Artifact } from "@/lib/daily";
import { isSvgName, markdownDocument, svgDocument } from "@/lib/markdown";
import { useStore } from "@/store/useStore";
import { ExtBadge, ReplyIcon, iconButton } from "./ui";
import type { Quote } from "./ChatComposer";

/**
 * The conversation's output, and one file at a time up close.
 *
 * Every turn shares the conversation's working directory, so this panel lists the
 * conversation rather than the message — which is also why a file someone attached
 * appears here beside the ones an agent wrote. From inside the container they are
 * the same thing: contents of /work.
 *
 * Previewing follows the rules the Daily gallery already established, and for the
 * same reason: the bytes were written by an agent, so media renders inline and
 * everything else is read as data. Markdown and HTML go into a `sandbox=""`
 * iframe, which can run nothing and reach nothing — the app's own origin is the
 * one holding the loopback services, and that is not a place to render agent
 * output.
 */

/** Width of the drag handle. It lives inside the collapsing wrapper so it goes
 *  away with the panel instead of leaving a stray divider behind. */
const HANDLE_W = 5;

type Format = "markdown" | "html" | "plain";

function formatOf(name: string): Format {
  const ext = name.toLowerCase().slice(name.lastIndexOf("."));
  if (ext === ".md" || ext === ".markdown") return "markdown";
  if (ext === ".html" || ext === ".htm") return "html";
  return "plain";
}

/** A region marked on an image or a frame, in percentages of the box. A click
 *  with no drag is a point, which reads as "this bit here" and is what people
 *  actually do most of the time. */
interface Region {
  x: number;
  y: number;
  w: number;
  h: number;
  point: boolean;
}

function Annotator({
  children,
  onReady,
}: {
  children: React.ReactNode;
  onReady: (r: Region) => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [from, setFrom] = useState<{ x: number; y: number } | null>(null);
  const [rect, setRect] = useState<Region | null>(null);

  const pos = (e: React.MouseEvent) => {
    const box = ref.current?.getBoundingClientRect();
    if (!box) return { x: 0, y: 0 };
    return {
      x: Math.max(0, Math.min(100, ((e.clientX - box.left) / box.width) * 100)),
      y: Math.max(0, Math.min(100, ((e.clientY - box.top) / box.height) * 100)),
    };
  };

  return (
    <div
      ref={ref}
      onMouseDown={(e) => { setFrom(pos(e)); setRect(null); }}
      onMouseMove={(e) => {
        if (!from) return;
        const p = pos(e);
        setRect({
          x: Math.min(from.x, p.x), y: Math.min(from.y, p.y),
          w: Math.abs(p.x - from.x), h: Math.abs(p.y - from.y), point: false,
        });
      }}
      onMouseUp={(e) => {
        if (!from) return;
        const p = pos(e);
        const w = Math.abs(p.x - from.x);
        const h = Math.abs(p.y - from.y);
        const region: Region = w < 2 || h < 2
          ? { x: p.x, y: p.y, w: 0, h: 0, point: true }
          : { x: Math.min(from.x, p.x), y: Math.min(from.y, p.y), w, h, point: false };
        setFrom(null);
        setRect(region);
        onReady(region);
      }}
      style={{ position: "relative", cursor: "crosshair", userSelect: "none", overflow: "hidden", borderRadius: 12 }}
    >
      {children}
      {rect && (
        <div
          style={{
            position: "absolute", pointerEvents: "none",
            left: `${rect.x}%`, top: `${rect.y}%`,
            width: rect.point ? 12 : `${rect.w}%`,
            height: rect.point ? 12 : `${rect.h}%`,
            marginLeft: rect.point ? -6 : 0, marginTop: rect.point ? -6 : 0,
            border: "1.5px solid var(--ac)", borderRadius: rect.point ? "50%" : 4,
            background: rect.point ? "var(--ac)" : "rgba(79,157,255,.14)",
          }}
        />
      )}
    </div>
  );
}

export function ChatPanel({
  conversationId,
  artifacts,
  open,
  width,
  dragging,
  file,
  onOpenFile,
  onCloseFile,
  onToggle,
  onQuote,
  onResizeStart,
  onResizeReset,
}: {
  conversationId: string | null;
  artifacts: Artifact[];
  open: boolean;
  width: number;
  /** The handle is being dragged, so easing is off — see the note by the shell. */
  dragging: boolean;
  file: Artifact | null;
  onOpenFile: (a: Artifact) => void;
  onCloseFile: () => void;
  onToggle: () => void;
  onQuote: (q: Omit<Quote, "id">) => void;
  onResizeStart: (e: React.MouseEvent) => void;
  onResizeReset: () => void;
}) {
  const { t } = useTranslation();
  const theme = useStore((s) => s.theme);
  const [text, setText] = useState<string | null>(null);
  const [region, setRegion] = useState<Region | null>(null);
  const [note, setNote] = useState("");
  const videoRef = useRef<HTMLVideoElement>(null);
  const [mark, setMark] = useState<number | null>(null);

  const src = conversationId && file ? chat.artifactUrl(conversationId, file.path) : "";
  const format = file ? formatOf(file.name) : "plain";

  useEffect(() => { setText(null); setRegion(null); setNote(""); setMark(null); }, [file?.path]);

  const svg = !!file && isSvgName(file.name);

  useEffect(() => {
    // An SVG is fetched for the same reason text is: the route hands it back as
    // an attachment, so it has to be read as data and rendered here.
    if (!file || (file.kind !== "text" && !isSvgName(file.name)) || !src) return;
    let cancelled = false;
    // Text is fetched rather than embedded: the host serves it as an attachment
    // (only media may render inline), so it arrives as data and is displayed here.
    fetch(src)
      .then((r) => r.text())
      .then((v) => { if (!cancelled) setText(v); })
      .catch(() => { if (!cancelled) setText(t("daily.readFailed")); });
    return () => { cancelled = true; };
  }, [src, file, t]);

  const doc = useMemo(() => {
    if (text == null) return null;
    if (svg) return svgDocument(text, theme);
    return format === "markdown" ? markdownDocument(text, theme) : text;
  }, [text, format, theme, svg]);

  const regionLabel = (r: Region) =>
    r.point
      ? t("chat.panel.point", { x: Math.round(r.x), y: Math.round(r.y) })
      : t("chat.panel.region", { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.w), h: Math.round(r.h) });

  const submitNote = () => {
    if (!file || !note.trim()) return;
    const time = mark != null ? ` · ${Math.floor(mark / 60)}:${String(Math.floor(mark % 60)).padStart(2, "0")}` : "";
    onQuote({
      from: file.name,
      loc: `${region ? regionLabel(region) : ""}${time}`.trim(),
      text: note.trim(),
    });
    setNote("");
    setRegion(null);
    setMark(null);
  };

  // The panel stays mounted and collapses to zero width, rather than being
  // unmounted: an element that is not there cannot animate, and swapping it in
  // and out also threw away the preview's scroll position and re-fetched the
  // file being read.
  //
  // The contents are held at their full width inside the collapsing wrapper
  // (`overflow: hidden`), so nothing reflows on the way: a list that re-wraps on
  // every frame of a 220ms transition is what "not smooth" looks like. The same
  // shape the left rail uses.
  //
  // The transition is dropped while the handle is being dragged. A width that
  // eases toward its target cannot keep up with a cursor, and the panel lags
  // behind the pointer for as long as the easing lasts.
  const ease = "cubic-bezier(.2,.8,.2,1)";
  const shellWidth = open ? width + HANDLE_W : 0;

  return (
    <div
      style={{
        width: shellWidth, flex: "none", minWidth: 0, overflow: "hidden",
        display: "flex", minHeight: 0,
        transition: dragging ? "none" : `width .22s ${ease}`,
        willChange: dragging ? undefined : "width",
      }}
    >
      <div
        onMouseDown={onResizeStart}
        onDoubleClick={onResizeReset}
        title={t("chat.panel.resize")}
        style={{ width: HANDLE_W, flex: "none", cursor: "col-resize", display: "flex", alignItems: "center", justifyContent: "center", background: "transparent" }}
      >
        <div style={{ width: 1, height: "100%", background: "var(--bd)" }} />
      </div>

      <div
        style={{
          width, flex: "none", minWidth: 0, background: "var(--bg-panel)",
          display: "flex", flexDirection: "column", minHeight: 0,
          // Fades a touch behind the width so the edge does not read as a hard
          // clip; on the way in it lands after the panel has somewhere to be.
          opacity: open ? 1 : 0,
          transition: dragging ? "none" : `opacity .18s ${ease} ${open ? ".04s" : "0s"}`,
        }}
      >
        {!file ? (
          <>
            <div style={{ padding: "22px 20px 10px", display: "flex", alignItems: "center", gap: 8 }}>
              <span style={{ font: "600 9.5px 'IBM Plex Mono'", letterSpacing: ".1em", color: "var(--tx-faint)" }}>
                {t("chat.panel.outputs")}
              </span>
              <span style={{ font: "500 9.5px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>{artifacts.length}</span>
              <div style={{ flex: 1 }} />
              <div onClick={onToggle} title={t("chat.panel.hide")} style={iconButton()}>
                <span style={{ font: "400 13px 'IBM Plex Sans'" }}>✕</span>
              </div>
            </div>
            <div style={{ flex: 1, overflowY: "auto", padding: "4px 12px 16px", display: "flex", flexDirection: "column", gap: 2 }}>
              {artifacts.map((a) => (
                <div
                  key={a.path}
                  onClick={() => onOpenFile(a)}
                  style={{ display: "flex", alignItems: "center", gap: 11, padding: "9px 8px", borderRadius: 10, cursor: "pointer" }}
                >
                  <ExtBadge name={a.name} kind={a.kind} />
                  <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: 2 }}>
                    <span style={{ font: "500 11.5px 'IBM Plex Mono'", color: "var(--tx2)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{a.name}</span>
                    <span style={{ font: "400 9.5px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>
                      {t(`chat.panel.kind.${a.kind}`)} · {formatSize(a.size)}
                    </span>
                  </div>
                </div>
              ))}
              {artifacts.length === 0 && (
                <span style={{ font: "400 11px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "14px 6px", lineHeight: 1.7 }}>
                  {t("chat.panel.empty")}
                </span>
              )}
            </div>
          </>
        ) : (
          <>
            <div style={{ animation: "ocFade .16s ease-out both", padding: "18px 16px 6px", display: "flex", alignItems: "center", gap: 8 }}>
              <div onClick={onCloseFile} style={{ font: "500 14px 'IBM Plex Sans'", color: "var(--tx3)", cursor: "pointer", padding: "0 4px" }}>‹</div>
              <ExtBadge name={file.name} kind={file.kind} size={26} />
              <span style={{ font: "500 11.5px 'IBM Plex Mono'", color: "var(--tx)", flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {file.name}
              </span>
              {/* Download rather than "open in the editor": the Workspace editor
                  works on a repository worktree, and a conversation's directory is
                  not one. Offering a button that cannot open the file would be
                  worse than offering the one that always works. */}
              <a
                href={conversationId ? chat.artifactUrl(conversationId, file.path, true) : "#"}
                download={file.name}
                style={{ font: "600 10px 'IBM Plex Sans'", color: "var(--tx3)", background: "var(--bg-card)", padding: "5px 10px", borderRadius: 14, cursor: "pointer", textDecoration: "none" }}
              >
                {t("chat.panel.download")}
              </a>
            </div>
            <div style={{ padding: "0 20px 8px 46px", display: "flex", alignItems: "center", gap: 6 }}>
              <span style={{ font: "400 9.5px 'IBM Plex Mono'", color: "var(--tx-faint)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {file.path} · {formatSize(file.size)}
              </span>
            </div>

            <div style={{ animation: "ocRise .2s cubic-bezier(.2,.8,.2,1) both", flex: 1, overflowY: "auto", padding: "10px 18px 20px", display: "flex", flexDirection: "column", gap: 9 }}>
              {file.kind === "image" && (
                <Annotator onReady={setRegion}>
                  {svg ? (
                    // pointer-events off so the drag lands on the annotator above
                    // it: the picture is static, nothing inside it wants a click.
                    <iframe
                      title={file.name}
                      srcDoc={doc ?? ""}
                      sandbox=""
                      style={{ display: "block", width: "100%", height: 260, border: 0, pointerEvents: "none", background: "var(--bg-deep)" }}
                    />
                  ) : (
                    <img src={src} alt={file.name} style={{ display: "block", width: "100%", background: "var(--bg-deep)" }} />
                  )}
                </Annotator>
              )}

              {file.kind === "video" && (
                <div style={{ position: "relative" }}>
                  <video ref={videoRef} src={src} controls style={{ display: "block", width: "100%", borderRadius: 12, background: "#050608" }} />
                  {/* The overlay stops short of the control strip so the player
                      stays usable while a region can still be marked on the frame. */}
                  <div style={{ position: "absolute", left: 0, right: 0, top: 0, bottom: 44 }}>
                    <Annotator
                      onReady={(r) => { setRegion(r); setMark(videoRef.current?.currentTime ?? null); }}
                    >
                      <div style={{ width: "100%", height: "100%" }} />
                    </Annotator>
                  </div>
                </div>
              )}

              {file.kind === "audio" && <audio src={src} controls style={{ width: "100%" }} />}

              {file.kind === "pdf" && (
                <iframe title={file.name} src={src} style={{ width: "100%", height: 520, border: 0, borderRadius: 12, background: "#fff" }} />
              )}

              {file.kind === "text" && (
                format === "plain" ? (
                  <pre style={{ margin: 0, overflow: "auto", font: "400 11.5px/1.6 'IBM Plex Mono'", color: "var(--tx2)", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                    {text ?? t("common.loading")}
                  </pre>
                ) : (
                  <iframe
                    title={file.name}
                    srcDoc={doc ?? ""}
                    sandbox=""
                    style={{ width: "100%", height: 520, border: 0, borderRadius: 12, display: "block", background: format === "html" ? "#fff" : "var(--bg-deep)" }}
                  />
                )
              )}

              {file.kind === "file" && (
                <span style={{ font: "400 11.5px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "24px 0", textAlign: "center" }}>
                  {t("daily.noPreview")}
                </span>
              )}

              {(file.kind === "image" || file.kind === "video") && (
                region ? (
                  <div style={{ animation: "ocRise .18s cubic-bezier(.2,.8,.2,1) both", display: "flex", flexDirection: "column", gap: 8, background: "var(--bg-card)", borderRadius: 12, padding: "10px 11px" }}>
                    <span style={{ font: "500 10px 'IBM Plex Mono'", color: "var(--tx-dim)" }}>
                      {regionLabel(region)}
                      {mark != null && ` · ${Math.floor(mark / 60)}:${String(Math.floor(mark % 60)).padStart(2, "0")}`}
                    </span>
                    <textarea
                      value={note}
                      onChange={(e) => setNote(e.target.value)}
                      onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); submitNote(); } }}
                      placeholder={t("chat.panel.commentPlaceholder")}
                      rows={2}
                      style={{ resize: "none", background: "var(--bg-deep)", border: "none", outline: "none", borderRadius: 9, padding: "8px 10px", font: "400 12px 'IBM Plex Sans'", color: "var(--tx)", lineHeight: 1.6, fontFamily: "'IBM Plex Sans', sans-serif" }}
                    />
                    <div style={{ display: "flex", alignItems: "center", gap: 6, justifyContent: "flex-end" }}>
                      <div onClick={() => { setRegion(null); setNote(""); }} style={{ font: "500 11px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "5px 10px", borderRadius: 14, cursor: "pointer" }}>
                        {t("chat.panel.cancel")}
                      </div>
                      <div onClick={submitNote} style={{ display: "flex", alignItems: "center", gap: 6, font: "600 11px 'IBM Plex Sans'", color: "var(--tx)", background: "var(--bg-card2)", padding: "5px 12px", borderRadius: 14, cursor: "pointer" }}>
                        <ReplyIcon color="currentColor" />
                        {t("chat.panel.reply")}
                      </div>
                    </div>
                  </div>
                ) : (
                  <span style={{ font: "400 10px 'IBM Plex Sans'", color: "var(--tx-faint)" }}>{t("chat.panel.annotateHint")}</span>
                )
              )}

              {file.kind === "text" && (
                <div style={{ display: "flex", justifyContent: "flex-end" }}>
                  <div
                    onClick={() => text && onQuote({ from: file.name, text: text.slice(0, 600) })}
                    style={{ display: "flex", alignItems: "center", gap: 6, font: "500 10.5px 'IBM Plex Sans'", color: "var(--tx-dim)", padding: "5px 10px", borderRadius: 14, cursor: "pointer" }}
                  >
                    <ReplyIcon />
                    {t("chat.panel.quoteFile")}
                  </div>
                </div>
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
