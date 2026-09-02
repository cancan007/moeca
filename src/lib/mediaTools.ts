// The shipped generation tools, and the migration off the grant that preceded
// them.
//
// Image, speech and video generation used to be three switches on an agent,
// backed by hand-written code in one vendor's request and response shape. The
// route was not even separately configurable — it followed whichever provider
// the agent reasoned with, so asking Claude to draw meant posting an OpenAI
// image model to /anthropic/v1/images/generations, which does not exist.
//
// They are ordinary tools now. What was a switch is a tool the agent is granted
// like any other, what was hardcoded is a field you can edit, and a provider
// that spells its API differently is a tool definition rather than a patch.
import i18n from "@/i18n";
import type { ToolDef } from "@/lib/tools";
import type { SoloAgent } from "@/lib/templates";
import type { MediaSpec, MediaTools } from "@/lib/sandbox";

/** Ids of the shipped generation tools. Stable, because an agent's toolIds
 *  point at them. */
export const MEDIA_TOOL_IDS = {
  image: "gen-image",
  speech: "gen-speech",
  video: "gen-video",
} as const;

/** The edit preset is not one of the three grants: it starts from a picture the
 *  run already has rather than from nothing, so it is an ordinary tool an agent
 *  is given, and it has no counterpart in the old media grant. */
export const EDIT_IMAGE_TOOL_ID = "edit-image";

/** Uploading a file so a later call can name it by id.
 *
 *  Not a generation grant: it makes nothing. It exists because one route —
 *  video — takes its reference picture as an id rather than as a form part, and
 *  an agent with no way to obtain an id cannot use a reference at all. */
export const UPLOAD_FILE_TOOL_ID = "upload-file";

export const MEDIA_EXTENSIONS = {
  image: [".png", ".jpg", ".jpeg", ".webp"],
  speech: [".mp3", ".wav", ".opus", ".flac"],
  video: [".mp4", ".webm"],
} as const;

type Kind = keyof typeof MEDIA_TOOL_IDS;

/** The default generation route. Every shipped model below is OpenAI's, so a
 *  preset that pointed anywhere else would ship broken. */
const DEFAULT_PREFIX = "/openai/";

const EDIT_PATH = "/v1/images/edits";

/** The request path each kind uses after the provider prefix. */
const DEFAULT_PATH: Record<Kind, string> = {
  image: "/v1/images/generations",
  speech: "/v1/audio/speech",
  video: "/v1/videos",
};

const DEFAULT_MODEL: Record<Kind, string> = {
  image: "gpt-image-1",
  speech: "gpt-4o-mini-tts",
  video: "sora-2",
};

/** The three presets, as editable tool definitions. Descriptions come from the
 *  active locale at build time — these are seeded into the tool list, where the
 *  operator owns them from then on. */
export function mediaToolPresets(prefix = DEFAULT_PREFIX): ToolDef[] {
  return [
    {
      id: MEDIA_TOOL_IDS.image,
      name: "generate_image",
      description: i18n.t("tools.media.image.description"),
      params: [
        { name: "prompt", type: "string", description: i18n.t("tools.media.image.prompt"), required: true },
        { name: "size", type: "string", description: i18n.t("tools.media.image.size"), required: false },
      ],
      method: "POST",
      path: prefix.replace(/\/$/, "") + DEFAULT_PATH.image,
      headers: {},
      body: `{"model":"${DEFAULT_MODEL.image}","prompt":"{{prompt}}","size":"{{size}}","n":1}`,
      targetHeader: "",
      output: { kind: "base64", jsonPath: "data.0.b64_json", extensions: [...MEDIA_EXTENSIONS.image] },
    },
    {
      id: MEDIA_TOOL_IDS.speech,
      name: "generate_speech",
      description: i18n.t("tools.media.speech.description"),
      params: [
        { name: "text", type: "string", description: i18n.t("tools.media.speech.text"), required: true },
        { name: "voice", type: "string", description: i18n.t("tools.media.speech.voice"), required: false },
      ],
      method: "POST",
      path: prefix.replace(/\/$/, "") + DEFAULT_PATH.speech,
      headers: {},
      // response_format is {{ext}} — the extension of the file being written —
      // so the provider cannot be told a format that disagrees with the name
      // the artifact lands under.
      body: `{"model":"${DEFAULT_MODEL.speech}","input":"{{text}}","voice":"{{voice}}","response_format":"{{ext}}"}`,
      targetHeader: "",
      defaults: { voice: "alloy" },
      output: { kind: "binary", extensions: [...MEDIA_EXTENSIONS.speech] },
    },
    {
      id: MEDIA_TOOL_IDS.video,
      name: "generate_video",
      description: i18n.t("tools.media.video.description"),
      params: [
        { name: "prompt", type: "string", description: i18n.t("tools.media.video.prompt"), required: true },
        { name: "seconds", type: "string", description: i18n.t("tools.media.video.seconds"), required: false },
        { name: "size", type: "string", description: i18n.t("tools.media.image.size"), required: false },
        { name: "file_id", type: "string", description: i18n.t("tools.media.video.fileId"), required: false },
      ],
      method: "POST",
      path: prefix.replace(/\/$/, "") + DEFAULT_PATH.video,
      headers: {},
      // input_reference is an OBJECT, not a file — the one place the video
      // route differs from the edit routes beside it. Posting the picture as a
      // form part is rejected ("expected an object, but got a file"), so the
      // file is uploaded first with upload_file and named here by its id. The
      // whole object disappears when no id is supplied, which is what makes the
      // same tool still do text-to-video.
      body: `{"model":"${DEFAULT_MODEL.video}","prompt":"{{prompt}}","seconds":"{{seconds}}","size":"{{size}}","input_reference":{"file_id":"{{file_id}}"}}`,
      targetHeader: "",
      output: {
        kind: "binary",
        extensions: [...MEDIA_EXTENSIONS.video],
        poll: {
          idPath: "id", statusPath: "status", errorPath: "error",
          done: ["completed"], fail: ["failed", "cancelled"],
          statusUrl: "/{{id}}", resultUrl: "/{{id}}/content",
          // Video queues before it renders, and a queue is not a failure. The
          // generic 15-minute default gave up on a job that had not started —
          // the run reported failure while the work was still pending.
          forSec: 45 * 60,
        },
      },
    },
    {
      id: UPLOAD_FILE_TOOL_ID,
      name: "upload_file",
      description: i18n.t("tools.media.upload.description"),
      params: [
        { name: "file", type: "string", description: i18n.t("tools.media.upload.file"), required: true },
        { name: "purpose", type: "string", description: i18n.t("tools.media.upload.purpose"), required: false },
      ],
      method: "POST",
      path: prefix.replace(/\/$/, "") + "/v1/files",
      headers: {},
      body: `{"purpose":"{{purpose}}"}`,
      targetHeader: "",
      defaults: { purpose: "vision" },
      inputs: { file: { as: "multipart", field: "file" } },
      // Text, deliberately: the response is what the model needs to read. The
      // id it returns is the only way to name this picture in a later call.
    },
    {
      id: EDIT_IMAGE_TOOL_ID,
      name: "edit_image",
      description: i18n.t("tools.media.edit.description"),
      params: [
        { name: "image", type: "string", description: i18n.t("tools.media.edit.image"), required: true },
        { name: "prompt", type: "string", description: i18n.t("tools.media.edit.prompt"), required: true },
        { name: "size", type: "string", description: i18n.t("tools.media.image.size"), required: false },
      ],
      method: "POST",
      path: prefix.replace(/\/$/, "") + EDIT_PATH,
      headers: {},
      // The same body template as generation; it becomes the form fields once a
      // file is attached, so the tool reads the same either way.
      body: `{"model":"${DEFAULT_MODEL.image}","prompt":"{{prompt}}","size":"{{size}}"}`,
      targetHeader: "",
      inputs: { image: { as: "multipart", field: "image" } },
      output: { kind: "base64", jsonPath: "data.0.b64_json", extensions: [...MEDIA_EXTENSIONS.image] },
    },
  ];
}

/** A grant migrated into a tool: the model and route it named are preserved, so
 *  a working configuration keeps working and a broken one stays visible rather
 *  than being silently repaired into something the operator never chose. */
function fromGrant(kind: Kind, spec: MediaSpec, preset: ToolDef): ToolDef {
  const prefix = spec.prefix?.endsWith("/") ? spec.prefix : `${spec.prefix ?? ""}/`;
  // The grant's route is prefix + path, and an unset path meant the per-kind
  // default — so the prefix has to apply either way. Dropping back to the
  // preset's whole path when only the path was unset would silently move the
  // agent onto a different provider than the one it was configured with.
  const path = prefix.replace(/\/$/, "") + (spec.path || DEFAULT_PATH[kind]);
  const body = spec.model ? preset.body.replace(/"model":"[^"]*"/, `"model":"${spec.model}"`) : preset.body;
  const defaults = { ...preset.defaults };
  if (kind === "image" && spec.size) defaults.size = spec.size;
  if (kind === "speech" && spec.voice) defaults.voice = spec.voice;
  if (kind === "video" && spec.seconds) defaults.seconds = spec.seconds;
  return { ...preset, path, body, defaults };
}

/** One-time migration: turn each agent's media grants into tools it is granted.
 *
 *  Returns the updated tools and solos, or null when there is nothing to move —
 *  the caller writes nothing in that case, so this cannot churn stored state on
 *  every load. */
export function migrateMediaGrants(
  solos: SoloAgent[],
  tools: ToolDef[],
): { solos: SoloAgent[]; tools: ToolDef[] } | null {
  if (!solos.some((s) => s.media && Object.keys(s.media).length > 0)) return null;

  const presets = mediaToolPresets();
  const byId = new Map(tools.map((t) => [t.id, t]));
  const nextSolos = solos.map((s) => {
    if (!s.media) return s;
    const ids = new Set(s.toolIds ?? []);
    for (const kind of ["image", "speech", "video"] as Kind[]) {
      const spec = (s.media as MediaTools)[kind];
      if (!spec) continue;
      const id = MEDIA_TOOL_IDS[kind];
      // The first agent to claim a kind decides the tool's shape. Two agents
      // with different image models is a case the grant could express and a
      // shared tool cannot — rare enough to accept, and visible in Settings →
      // Tools rather than hidden inside each agent.
      if (!byId.has(id)) {
        byId.set(id, fromGrant(kind, spec, presets.find((p) => p.id === id)!));
      }
      ids.add(id);
    }
    return { ...s, media: undefined, toolIds: [...ids] };
  });
  return { solos: nextSolos, tools: [...byId.values()] };
}
