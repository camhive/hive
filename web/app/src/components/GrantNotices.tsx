import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useGrantNotices, type GrantNotice } from "@/lib/sharing";

/**
 * One notice per grant that ends within 3 days or ended less than 3 days
 * ago, for the owner (with a way to the hive's sharing page) and for the
 * member (#85). The cloud decides the window; nothing shows otherwise.
 */
export function GrantNotices() {
  const { t } = useTranslation();
  const notices = useGrantNotices();
  if (!notices.data?.length) return null;
  return (
    <section className="card space-y-2" aria-label={t("grantNotice.title")}>
      <ul className="text-sm divide-y divide-[var(--border)]">
        {notices.data.map((n) => <Notice key={n.grant_id} n={n} />)}
      </ul>
    </section>
  );
}

function Notice({ n }: { n: GrantNotice }) {
  const { t } = useTranslation();
  const camera = n.camera_name || (n.camera_id === "*" ? t("grantNotice.allCameras") : n.camera_id);
  const owner = n.side === "owner";
  const key = `grantNotice.${n.side}${n.state === "expired" ? "Expired" : "Expiring"}`;
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 py-2" data-testid="grant-notice" data-state={n.state}>
      <span>{t(key, { camera, hive: n.hive_name, peer: n.peer, at: new Date(n.expires_at).toLocaleString() })}</span>
      {owner && (
        <Link to="/hives/$id/sharing" params={{ id: n.hive_id }} className="link">
          {t(n.state === "expired" ? "grantNotice.renew" : "grantNotice.extend")}
        </Link>
      )}
    </li>
  );
}
