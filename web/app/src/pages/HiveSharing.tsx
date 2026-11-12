import { useState, type FormEvent } from "react";
import { useParams } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { CamhiveError } from "@camhive/api";
import { useMe } from "@/lib/session";
import { useHives } from "@/lib/hives";
import { useAccess } from "@/lib/access";
import { useCameras, useHiveClient } from "@/lib/hiveApi";
import { useGrants, useMembers, useSharingActions } from "@/lib/sharing";
import { errorMessage } from "@/lib/errors";
import { HiveNav } from "@/components/HiveNav";
import { PlanLimitBox } from "@/components/PlanLimitBox";

/** Who may see which camera of this hive, and as what (#54). Owners only. */
export function HiveSharing() {
  const { t } = useTranslation();
  const { id } = useParams({ from: "/hives/$id/sharing" });
  const me = useMe();
  const hives = useHives();
  const hive = hives.data?.hives.find((h) => h.id === id);
  const shared = !hive && !!hives.data?.shared.find((h) => h.id === id);
  const grants = useGrants(hive?.id);
  const members = useMembers();
  const access = useAccess(hive?.id, { enabled: !!hive?.online });
  const client = useHiveClient(hive, access.data);
  const cameras = useCameras(hive, client);
  const actions = useSharingActions();
  const [memberId, setMemberId] = useState("");
  const [cameraId, setCameraId] = useState("*");
  const [role, setRole] = useState<"viewer" | "operator">("viewer");
  const [expires, setExpires] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [limit, setLimit] = useState(false);
  if (me.isPending || hives.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  if (shared) return <p className="card muted">{t("sharing.ownerOnly")}</p>;
  if (!hive) return <p className="card">{t("errors.not_found")}</p>;

  const cameraName = (cid: string) => (cid === "*" ? t("sharing.allCameras") : cameras.data?.find((c) => c.id === cid)?.name ?? cid);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLimit(false);
    try {
      await actions.setGrant(hive!.id, { member_id: memberId, camera_id: cameraId, role, ...(expires ? { expires_at: new Date(expires).toISOString() } : {}) });
    } catch (err) {
      if (err instanceof CamhiveError && err.code === "plan_limit") setLimit(true);
      else setError(errorMessage(t, err));
    }
  }
  const activeMembers = members.data?.members.filter((m) => m.status === "active") ?? [];
  return (
    <div className="space-y-4 max-w-none">
      <HiveNav hive={hive} active="sharing" />
      <section className="card" aria-label={t("sharing.grants")}>
        <h2 className="text-lg font-semibold mb-2">{t("sharing.grants")}</h2>
        {grants.data?.length === 0 && <p className="muted text-sm">{t("sharing.noGrants")}</p>}
        <ul className="text-sm divide-y divide-[var(--border)]">
          {grants.data?.map((g) => (
            <li key={g.id} className="flex flex-wrap items-center justify-between gap-3 py-2.5" data-testid="grant">
              <span>{g.member_email} · <span className="font-medium">{cameraName(g.camera_id)}</span> · {t(`sharing.role.${g.role}`)}{g.expires_at ? ` · ${t("sharing.until", { at: new Date(g.expires_at).toLocaleString() })}` : ""}</span>
              <button type="button" className="link link-danger" onClick={() => void actions.deleteGrant(hive.id, g.id)}>{t("sharing.revoke")}</button>
            </li>
          ))}
        </ul>
      </section>
      <form className="card grid gap-x-4 sm:grid-cols-2" onSubmit={(e) => void submit(e)} aria-label={t("sharing.addGrant")}>
        <h2 className="text-lg font-semibold sm:col-span-2 mb-1">{t("sharing.addGrant")}</h2>
        {activeMembers.length === 0 && <p className="muted text-sm sm:col-span-2 mb-3">{t("sharing.noMembers")}</p>}
        <div className="mb-4">
          <label className="block mb-1 text-sm font-medium" htmlFor="member">{t("sharing.member")}</label>
          <select id="member" className="input" required value={memberId} onChange={(e) => setMemberId(e.target.value)}>
            <option value="">—</option>
            {activeMembers.map((m) => <option key={m.id} value={m.id}>{m.email}</option>)}
          </select>
        </div>
        <div className="mb-4">
          <label className="block mb-1 text-sm font-medium" htmlFor="camera">{t("sharing.camera")}</label>
          <select id="camera" className="input" value={cameraId} onChange={(e) => setCameraId(e.target.value)}>
            <option value="*">{t("sharing.allCameras")}</option>
            {cameras.data?.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          {!hive.online && <span className="block mt-1 text-xs muted">{t("sharing.camerasOffline")}</span>}
        </div>
        <div className="mb-4">
          <label className="block mb-1 text-sm font-medium" htmlFor="role">{t("sharing.roleLabel")}</label>
          <select id="role" className="input" value={role} onChange={(e) => setRole(e.target.value as "viewer" | "operator")}>
            <option value="viewer">{t("sharing.role.viewer")}</option>
            <option value="operator">{t("sharing.role.operator")}</option>
          </select>
          <span className="block mt-1 text-xs muted">{t("sharing.roleHelp")}</span>
        </div>
        <div className="mb-4">
          <label className="block mb-1 text-sm font-medium" htmlFor="expires">{t("sharing.expires")}</label>
          <input id="expires" className="input" type="datetime-local" value={expires} onChange={(e) => setExpires(e.target.value)} />
        </div>
        {error && <p role="alert" className="error text-sm sm:col-span-2 mb-3">{error}</p>}
        {limit && <div className="sm:col-span-2"><PlanLimitBox entitlement="shares.max" titleKey="sharing.limitTitle" helpKey="sharing.limitHelp" /></div>}
        <div><button className="btn" disabled={!memberId}>{t("sharing.save")}</button></div>
      </form>
    </div>
  );
}
