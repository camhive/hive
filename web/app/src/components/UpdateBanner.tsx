import { useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { swUpdate, type SwUpdate } from "@/lib/swUpdate";

/** "A new version is available — Update" while a new app version waits; nothing otherwise (#70). */
export function UpdateBanner({ store = swUpdate }: { store?: SwUpdate }) {
  const { t } = useTranslation();
  const state = useSyncExternalStore(store.subscribe, store.snapshot);
  // The live region stays mounted and empty: screen readers skip text that arrives together with a new region.
  // aria-live, not role="status": pages keep their own status messages to themselves.
  if (state === "current") return <div aria-live="polite" data-testid="app-update" />;
  return (
    <div aria-live="polite" className="flex flex-wrap items-center justify-center gap-x-3 gap-y-1 px-4 py-2 text-sm" style={{ background: "var(--accent-soft)", color: "var(--accent-text)", borderBottom: "1px solid var(--border)" }} data-testid="app-update">
      <span className="font-medium">{t("appUpdate.available")}</span>
      <button type="button" className="btn btn-sm" disabled={state === "requested"} onClick={store.tap}>{t(state === "requested" ? "appUpdate.updating" : "appUpdate.update")}</button>
    </div>
  );
}
