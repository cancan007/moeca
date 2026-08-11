import { describe, it, expect } from "vitest";
import { migrateMediaGrants, mediaToolPresets, MEDIA_TOOL_IDS } from "./mediaTools";
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

  it("constrain the extension a generated file may land under", () => {
    for (const t of mediaToolPresets()) {
      expect(t.output?.extensions?.length).toBeGreaterThan(0);
    }
  });
});
