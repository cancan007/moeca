import { describe, it, expect } from "vitest";
import i18n, { languages } from "@/i18n";
import { defaultSolos, defaultGlobalPrompt, archExample } from "@/lib/templates";

describe("i18n smoke", () => {
  it("renders every language without falling back or leaving raw keys", async () => {
    for (const { code } of languages) {
      await i18n.changeLanguage(code);
      // literal {{param}} in a tool field label must survive interpolation
      const path = i18n.t("settings.tools.fieldPath", { tpl: "{{param}}" });
      expect(path).toContain("{{param}}");
      // counts interpolate
      expect(i18n.t("daily.countUnit", { count: 3 })).toContain("3");
      // Trans-style markup keys keep their tags for the component mapping
      expect(i18n.t("settings.rag.mountNote")).toContain("<b>");
      // prompts/templates follow the language
      expect(defaultGlobalPrompt(code).length).toBeGreaterThan(20);
      expect(archExample("plan", code).length).toBeGreaterThan(20);
      expect(defaultSolos(code)[0].role.length).toBeGreaterThan(0);
      // no key ever resolves to the key itself
      for (const k of ["nav.notifications", "delivery.inbox", "audit.tabs.logs", "knowledge.title", "review.tabs.task"]) {
        expect(i18n.t(k)).not.toBe(k);
      }
    }
    await i18n.changeLanguage("ja");
  });

  it("keeps the three dictionaries at the same key set", async () => {
    const flat = (o: any, p = ""): string[] =>
      Object.entries(o).flatMap(([k, v]) =>
        typeof v === "object" && v !== null ? flat(v, `${p}${k}.`) : [`${p}${k}`]);
    const { ja } = await import("@/i18n/locales/ja");
    const { en } = await import("@/i18n/locales/en");
    const { zh } = await import("@/i18n/locales/zh");
    const jk = flat(ja).sort();
    expect(flat(en).sort()).toEqual(jk);
    expect(flat(zh).sort()).toEqual(jk);
    expect(jk.length).toBeGreaterThan(400);
  });
});
