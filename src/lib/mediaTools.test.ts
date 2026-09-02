import { describe, it, expect } from "vitest";
import { migrateMediaGrants, mediaToolPresets, MEDIA_TOOL_IDS, EDIT_IMAGE_TOOL_ID, UPLOAD_FILE_TOOL_ID } from "./mediaTools";
import type { SoloAgent } from "@/lib/templates";
import type { ToolDef } from "@/lib/tools";

// Moving generation from a per-agent grant onto ordinary tools has to carry
// working configurations across untouched, and must not quietly "fix" a broken
// one — a route silently rewritten is how the /anthropic/ image endpoint went
// unnoticed for as long as it did.

const solo = (id: string, media?: SoloAgent["media"], toolIds?: string[]): SoloAgent =>
  ({ id, name: id, role: "", providerId: "anthropic", model: "m", ctx: "", strat: "", dot: "", arch: "generic", media, toolIds }) as SoloAgent;

describe("migrating media grants onto tools", () => {
  it("does nothing when no agent has a grant", () => {
    expect(migrateMediaGrants([solo("a"), solo("b")], [])).toBeNull();
  });

  it("turns a grant into a granted tool and clears the grant", () => {
    const before = [solo("designer", { image: { prefix: "/openai/", model: "gpt-image-1" } })];
    const out = migrateMediaGrants(before, [])!;

    expect(out.solos[0].media).toBeUndefined();
    expect(out.solos[0].toolIds).toContain(MEDIA_TOOL_IDS.image);
    const tool = out.tools.find((t) => t.id === MEDIA_TOOL_IDS.image)!;
    expect(tool.name).toBe("generate_image");
    expect(tool.output?.kind).toBe("base64");
  });

  // The grant's own route and model are what the operator configured, working
  // or not. Carrying them over means a broken one stays visible in the tool
  // editor instead of being replaced by something nobody chose.
  it("preserves the route and model the grant named, including a broken one", () => {
    const before = [solo("designer", { image: { prefix: "/anthropic/", model: "gpt-image-1" } })];
    const tool = migrateMediaGrants(before, [])!.tools.find((t) => t.id === MEDIA_TOOL_IDS.image)!;
    expect(tool.path.startsWith("/anthropic/")).toBe(true);
    expect(tool.body).toContain('"model":"gpt-image-1"');
  });

  it("carries per-kind defaults across", () => {
    const before = [solo("v", { speech: { prefix: "/openai/", model: "tts-1", voice: "echo" } })];
    const tool = migrateMediaGrants(before, [])!.tools.find((t) => t.id === MEDIA_TOOL_IDS.speech)!;
    expect(tool.defaults?.voice).toBe("echo");
  });

  // An agent's existing tools are grants too; the migration adds to them.
  it("keeps tools the agent already had", () => {
    const before = [solo("a", { video: { prefix: "/openai/", model: "sora-2" } }, ["slack"])];
    const ids = migrateMediaGrants(before, [])!.solos[0].toolIds!;
    expect(ids).toContain("slack");
    expect(ids).toContain(MEDIA_TOOL_IDS.video);
  });

  // A tool the operator already edited under that id wins: the migration must
  // not overwrite a definition they own.
  it("does not overwrite an existing tool definition", () => {
    const mine: ToolDef = {
      id: MEDIA_TOOL_IDS.image, name: "generate_image", description: "mine",
      params: [], method: "POST", path: "/vertex/predict", headers: {}, body: "{}", targetHeader: "",
      output: { kind: "base64", jsonPath: "predictions.0.bytesBase64Encoded" },
    };
    const out = migrateMediaGrants([solo("a", { image: { prefix: "/openai/", model: "x" } })], [mine])!;
    expect(out.tools.find((t) => t.id === MEDIA_TOOL_IDS.image)!.path).toBe("/vertex/predict");
  });
});

describe("shipped presets", () => {
  it("point at the provider that actually serves their models", () => {
    for (const t of mediaToolPresets()) {
      expect(t.path.startsWith("/openai/")).toBe(true);
    }
  });

  it("each declare where their bytes come from", () => {
    const [image, speech, video] = mediaToolPresets();
    expect(image.output).toMatchObject({ kind: "base64", jsonPath: "data.0.b64_json" });
    expect(speech.output?.kind).toBe("binary");
    // Video is the asynchronous one everywhere, so it polls.
    expect(video.output?.poll?.done).toEqual(["completed"]);
  });

  // Every preset that WRITES a file says what it may be called. Not every preset
  // writes one: upload_file hands its response back to the model, because the id
  // in it is the whole point of the call.
  it("constrain the extension a generated file may land under", () => {
    const writers = mediaToolPresets().filter((t) => t.output);
    expect(writers.length).toBeGreaterThan(0);
    for (const t of writers) {
      expect(t.output?.extensions?.length).toBeGreaterThan(0);
    }
  });

  // The video route names its reference picture by id rather than taking the
  // file, so the tool that produces an id has to exist alongside it — and the
  // reference has to vanish entirely when none is given, or text-to-video breaks.
  it("pair the video tool with the upload that feeds it", () => {
    const presets = mediaToolPresets();
    const upload = presets.find((t) => t.id === UPLOAD_FILE_TOOL_ID);
    expect(upload, "upload_file must ship").toBeTruthy();
    expect(upload?.inputs?.file?.as).toBe("multipart");
    expect(upload?.output, "upload_file returns its response, it does not write a file").toBeUndefined();

    const video = presets.find((t) => t.id === MEDIA_TOOL_IDS.video)!;
    expect(video.body).toContain('"input_reference":{"file_id":"{{file_id}}"}');
    expect(video.params.some((p) => p.name === "file_id" && !p.required)).toBe(true);
  });
});

// Starting from a picture the run already has, rather than from nothing. The
// providers differ on how the file travels, so the preset states it.
describe("the image-edit preset", () => {
  const edit = () => mediaToolPresets().find((t) => t.id === EDIT_IMAGE_TOOL_ID)!;

  it("declares its image parameter as an uploaded file", () => {
    expect(edit().inputs?.image).toMatchObject({ as: "multipart", field: "image" });
  });

  it("posts to the edit route, not the generation one", () => {
    expect(edit().path).toContain("/v1/images/edits");
  });

  // The body doubles as the form fields once a file is attached, so an edit
  // tool reads the same as a generation tool.
  it("keeps the same body template as generation", () => {
    expect(edit().body).toContain('"model"');
    expect(edit().body).toContain("{{prompt}}");
  });

  it("still writes its result as an image artifact", () => {
    expect(edit().output).toMatchObject({ kind: "base64", jsonPath: "data.0.b64_json" });
  });

  // It is a tool, not a fourth grant: the migration off media grants must not
  // invent one for an agent that never had it.
  it("is not produced by migrating a media grant", () => {
    const solo = { id: "a", name: "a", role: "", providerId: "anthropic", model: "m", ctx: "", strat: "", dot: "", arch: "generic",
      media: { image: { prefix: "/openai/", model: "gpt-image-1" } } } as SoloAgent;
    const out = migrateMediaGrants([solo], [])!;
    expect(out.solos[0].toolIds).not.toContain(EDIT_IMAGE_TOOL_ID);
  });
});

// A queue is not a failure. The generic 15-minute poll window gave up on a
// video job that had not started rendering, and the run reported failure for
// work that was still pending.
describe("the video preset's patience", () => {
  it("waits far longer than the generic default", () => {
    const video = mediaToolPresets().find((t) => t.id === MEDIA_TOOL_IDS.video)!;
    expect(video.output?.poll?.forSec ?? 0).toBeGreaterThanOrEqual(30 * 60);
  });

  // Image and speech return in seconds; only video queues.
  it("is the only preset that needs it", () => {
    for (const t of mediaToolPresets()) {
      if (t.id === MEDIA_TOOL_IDS.video) continue;
      expect(t.output?.poll).toBeUndefined();
    }
  });
});
