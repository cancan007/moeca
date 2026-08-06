// App-wide i18n.
//
// Japanese is the source locale: ja.ts is the only dictionary written from
// scratch, and en.ts / zh.ts are typed against it. A key added to ja but
// missing from the others is a compile error rather than a string that
// silently falls back to Japanese in a English or Chinese UI.
//
// One flat "translation" namespace with nested keys, so every call site is
// just `t("daily.title")` after a single argument-less useTranslation().

import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import LanguageDetector from "i18next-browser-languagedetector";

import { ja } from "./locales/ja";
import { en } from "./locales/en";
import { zh } from "./locales/zh";

/** localStorage key holding the chosen language (also read by the detector). */
export const LANG_KEY = "moeca.lang";

/** The languages the UI ships. Each label is written in its own language —
 *  someone who cannot read the current UI still recognises their own. */
export const languages = [
  { code: "ja", label: "日本語" },
  { code: "en", label: "English" },
  { code: "zh", label: "简体中文" },
] as const;

export type Lang = (typeof languages)[number]["code"];

export function isLang(v: string | undefined): v is Lang {
  return !!v && languages.some((l) => l.code === v);
}

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources: {
      ja: { translation: ja },
      en: { translation: en },
      zh: { translation: zh },
    },
    fallbackLng: "ja",
    supportedLngs: languages.map((l) => l.code),
    // A browser reporting "zh-CN" (or "en-US") resolves to the base bundle
    // instead of falling through to Japanese.
    load: "languageOnly",
    nonExplicitSupportedLngs: true,
    interpolation: { escapeValue: false }, // React escapes already
    detection: {
      order: ["localStorage", "navigator"],
      lookupLocalStorage: LANG_KEY,
      caches: ["localStorage"],
    },
    // Resources are bundled, so init resolves synchronously and nothing ever
    // needs to suspend on a pending load.
    react: { useSuspense: false },
  });

/** The active language, narrowed to one we actually ship. */
export function currentLang(): Lang {
  const l = i18n.resolvedLanguage ?? i18n.language;
  return isLang(l) ? l : "ja";
}

export default i18n;
