import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useHives, type Hive } from "@/lib/hives";
import { HiveStatus } from "@/components/HiveStatus";

/** Header shared by the hive pages: breadcrumb, name with status, optional controls, and the page tabs. */
export function HiveNav({ hive, active, actions }: { hive: Hive; active: "cameras" | "sharing" | "activity" | "settings"; actions?: React.ReactNode }) {
  const { t } = useTranslation();
  const owned = !!useHives().data?.hives.some((h) => h.id === hive.id); // the hive's action log is the owner's (#94)
  const tab = (key: typeof active, to: string, label: string) => (
    <Link to={to} params={{ id: hive.id }} className="tab" aria-current={key === active ? "page" : undefined}>{label}</Link>
  );
  return (
    <div className="min-w-0">
      <p className="text-sm muted flex items-center gap-1.5">
        <Link to="/" className="hover:underline underline-offset-2">{t("hives.title")}</Link>
        <span aria-hidden="true">/</span>
      </p>
      <div className="flex flex-wrap items-center justify-between gap-3 mt-0.5">
        <div className="flex min-w-0 items-center gap-3">
          <h1 className="page-title truncate">{hive.name}</h1>
          <HiveStatus hive={hive} />
        </div>
        {actions}
      </div>
      <nav className="tabs mt-3" aria-label={t("hives.pages")}>
        {tab("cameras", "/hives/$id", t("hives.cameras"))}
        {tab("sharing", "/hives/$id/sharing", t("hives.sharing"))}
        {owned && tab("activity", "/hives/$id/activity", t("hives.activity"))}
        {tab("settings", "/hives/$id/settings", t("hives.settings"))}
      </nav>
    </div>
  );
}
