import i18n from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";
import en from "./en.json";
import de from "./de.json";

export const resources = { en: { translation: en }, de: { translation: de } } as const;

export function setupI18n(lng?: string) {
  if (!i18n.isInitialized) {
    void i18n
      .use(LanguageDetector)
      .use(initReactI18next)
      .init({ resources, fallbackLng: "en", supportedLngs: ["en", "de"], interpolation: { escapeValue: false }, detection: { order: ["querystring", "localStorage", "navigator"], lookupLocalStorage: "camhive.lang" }, lng });
  }
  return i18n;
}

export default i18n;
