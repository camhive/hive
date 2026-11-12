import { useTranslation } from "react-i18next";
import type { Hive } from "@/lib/hives";

/** Online / offline / revoked pill for a hive. */
export function HiveStatus({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const revoked = hive.status === "revoked";
  const cls = revoked ? "badge badge-danger" : hive.online ? "badge badge-success" : "badge";
  return (
    <span className={cls} data-online={hive.online}>
      <span aria-hidden="true" className="dot" />
      {revoked ? t("hives.revoked") : hive.online ? t("hives.statusOnline") : t("hives.statusOffline")}
    </span>
  );
}
