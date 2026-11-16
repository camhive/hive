import { useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { unwrapHive, type HiveCamera, type HiveClient, type hivePaths } from "@camhive/api";
import { useMe } from "@/lib/session";
import { useHives } from "@/lib/hives";
import { useGrant } from "@/lib/useGrant";
import { useCameras, useHiveClient } from "@/lib/hiveApi";
import { errorMessage } from "@/lib/errors";
import { HiveNav } from "@/components/HiveNav";

type HiveAction = hivePaths["/v0/actions"]["get"]["responses"]["200"]["content"]["application/json"][number];

/** Rows per step; the API offers only a limit (newest first, at most MAX). */
export const PAGE = 50;
const MAX = 1000;

/** Actors the hive writes itself; an account id is "you" when it is the viewer's. */
const ACTORS = ["cloud", "updater", "hive", "console", "local"];

/** The hive's own action log (`GET /v0/actions`, #94): camera changes, exports, deletions, updates. Owners only, as on the hive. */
export function HiveActivity() {
  const { t } = useTranslation();
  const { id } = useParams({ from: "/hives/$id/activity" });
  const me = useMe();
  const hives = useHives();
  const hive = hives.data?.hives.find((h) => h.id === id);
  const shared = !hive && !!hives.data?.shared.find((h) => h.id === id);
  const { grant } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const cameras = useCameras(hive, client);
  const [limit, setLimit] = useState(PAGE);
  const actions = useHiveActions(client, id, limit);
  if (me.isPending || hives.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  if (shared) return <p className="card muted">{t("hiveActivity.ownerOnly")}</p>;
  if (!hive) return <p className="card">{t("errors.not_found")}</p>;
  const rows = actions.data ?? [];
  return (
    <div className="space-y-4 max-w-none">
      <HiveNav hive={hive} active="activity" />
      <p className="muted text-sm">{t("hiveActivity.intro")}</p>
      {actions.isError && <p role="alert" className="card error">{errorMessage(t, actions.error)}</p>}
      {actions.data && rows.length === 0 && <p className="card muted">{t("hiveActivity.empty")}</p>}
      {rows.length > 0 && (
        <ul className="card !py-2 divide-y divide-[var(--border)]" aria-label={t("hiveActivity.list")}>
          {rows.map((a) => <ActionRow key={a.id} a={a} me={me.data!.id} cameras={cameras.data ?? []} />)}
        </ul>
      )}
      {rows.length === limit && limit < MAX && (
        <button type="button" className="btn" disabled={actions.isFetching} onClick={() => setLimit(Math.min(limit + PAGE, MAX))}>{t("activity.more")}</button>
      )}
    </div>
  );
}

function useHiveActions(client: HiveClient | null, hiveId: string, limit: number) {
  return useQuery({
    queryKey: ["hive", hiveId, "actions", limit],
    queryFn: async (): Promise<HiveAction[]> => unwrapHive(await client!.GET("/v0/actions", { params: { query: { limit } } })),
    enabled: !!client,
    placeholderData: keepPreviousData, // "Load more" keeps the list on screen
  });
}

/**
 * What an action was done to, in words: cameras by name ("*" is all of
 * them, deletions list several with commas), an export by its camera.
 */
export function targetLabel(a: HiveAction, cameras: HiveCamera[], allCameras: string): string {
  const name = (id: string) => cameras.find((c) => c.id === id)?.name ?? id;
  const camera = a.details?.camera;
  if (a.action?.startsWith("export.")) return typeof camera === "string" ? name(camera) : "";
  if (a.target === "*") return allCameras;
  if (!a.target || a.target === "hive") return "";
  return a.target.split(",").map(name).join(", ");
}

/** The details without empty (null) values, as compact JSON. */
export function detailsText(details: HiveAction["details"]): string {
  const kept = Object.entries(details ?? {}).filter(([, v]) => v !== null && v !== undefined);
  return kept.length ? JSON.stringify(Object.fromEntries(kept)) : "";
}

function ActionRow({ a, me, cameras }: { a: HiveAction; me: string; cameras: HiveCamera[] }) {
  const { t } = useTranslation();
  const actor = a.actor === me ? t("hiveActivity.actors.you") : ACTORS.includes(a.actor ?? "") ? t(`hiveActivity.actors.${a.actor}`) : a.actor;
  const target = targetLabel(a, cameras, t("hiveActivity.allCameras"));
  const details = detailsText(a.details);
  return (
    <li className="py-3 text-sm flex flex-wrap justify-between gap-2" data-testid="hive-action" data-action={a.action}>
      <span>
        <span className="font-medium">{t(`hiveActivity.kinds.${a.action}`, { defaultValue: a.action })}</span>
        {target && <span className="muted"> · {target}</span>}
        {details && <span className="muted text-xs block break-all">{details}</span>}
      </span>
      <span className="muted whitespace-nowrap">{a.at ? new Date(a.at).toLocaleString() : ""} · {actor}</span>
    </li>
  );
}
