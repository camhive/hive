import { useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { installPrompt, type InstallPrompt } from "@/lib/install";

/** "Install Camhive" where the browser offers it, a hint on iOS, nothing once installed (#70). */
export function InstallCard({ store = installPrompt }: { store?: InstallPrompt }) {
  const { t } = useTranslation();
  const state = useSyncExternalStore(store.subscribe, store.snapshot);
  if (state.kind !== "available" && state.kind !== "ios-hint") return null;
  return (
    <section className="card" aria-labelledby="install-title">
      <h2 id="install-title" className="text-lg font-semibold mb-1">{t("install.title")}</h2>
      <p className="muted text-sm mb-3">{t(state.kind === "ios-hint" ? "install.iosHint" : "install.text")}</p>
      {state.kind === "available" && <button type="button" className="btn btn-sm" onClick={() => void store.install()}>{t("install.button")}</button>}
    </section>
  );
}
