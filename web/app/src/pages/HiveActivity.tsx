import { useState } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { unwrapHive, type HiveCamera, type HiveClient, type hivePaths } from "@camhive/api";
import { useMe } from "@/lib/session";
import { useHives } from "@/lib/hives";
import { useGrant } from "@/lib/useGrant";
import { useCameras, useHiveClient } from "@/lib/hiveApi";
import { errorMessage } from "@/lib/errors";
import { HiveNav } from "@/components/HiveNav";
import { KIND_GROUPS, filterOf, filterRows, searchOf, type ActivityFilter } from "@/lib/hiveActivity";

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
  const filter = filterOf(useSearch({ from: "/hives/$id/activity" }));
  const nav = useNavigate();
  const setFilter = (f: ActivityFilter) => void nav({ to: "/hives/$id/activity", params: { id }, search: searchOf(f), replace: true });
  if (me.isPending || hives.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  if (shared) return <p className="card muted">{t("hiveActivity.ownerOnly")}</p>;
  if (!hive) return <p className="card">{t("errors.not_found")}</p>;
  const rows = actions.data ?? [];
  const words = (a: HiveAction) => Object.values(describe(t, a, me.data!.id, cameras.data ?? [])).join(" ");
  const { shown, cloud } = filterRows(rows, filter, words);
  return (
    <div className="space-y-4 max-w-none">
      <HiveNav hive={hive} active="activity" />
      <p className="muted text-sm">{t("hiveActivity.intro")}</p>
      {actions.isError && <p role="alert" className="card error">{errorMessage(t, actions.error)}</p>}
      {actions.data && rows.length === 0 && <p className="card muted">{t("hiveActivity.empty")}</p>}
      {rows.length > 0 && <ActivityFilterBar filter={filter} cloud={cloud} onChange={setFilter} />}
      {rows.length > 0 && shown.length === 0 && <p className="card muted">{emptyFilter(t, filter, cloud, rows.length === limit && limit < MAX)}</p>}
      {shown.length > 0 && (
        <ul className="card !py-2 divide-y divide-[var(--border)]" aria-label={t("hiveActivity.list")}>
          {shown.map((a) => <ActionRow key={a.id} a={a} me={me.data!.id} cameras={cameras.data ?? []} />)}
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

type T = (key: string, o?: Record<string, unknown>) => string;

/** Why a filtered list is empty: only the hidden cloud sessions, or nothing matching; "Load more" only when there is more. */
function emptyFilter(t: T, f: ActivityFilter, cloud: number, more: boolean): string {
  if (f.kinds.length === 0 && !f.q.trim() && cloud > 0) return t("hiveActivity.filter.onlyCloud");
  return more ? `${t("hiveActivity.filter.none")} ${t("hiveActivity.filter.loadMore")}` : t("hiveActivity.filter.none");
}

/** A row in words: what the list shows and the filter's free text searches (#125). */
function describe(t: T, a: HiveAction, me: string, cameras: HiveCamera[]) {
  return {
    label: t(`hiveActivity.kinds.${a.action}`, { defaultValue: a.action }),
    target: targetLabel(a, cameras, t("hiveActivity.allCameras")),
    actor: a.actor === me ? t("hiveActivity.actors.you") : ACTORS.includes(a.actor ?? "") ? t(`hiveActivity.actors.${a.actor}`) : (a.actor ?? ""),
  };
}

/** Kind chips, free text, and the switch for the cloud's own sessions with how many there are. */
function ActivityFilterBar({ filter, cloud, onChange }: { filter: ActivityFilter; cloud: number; onChange: (f: ActivityFilter) => void }) {
  const { t } = useTranslation();
  const toggle = (g: (typeof KIND_GROUPS)[number]) =>
    onChange({ ...filter, kinds: filter.kinds.includes(g) ? filter.kinds.filter((k) => k !== g) : [...filter.kinds, g] });
  return (
    <div className="space-y-3" data-testid="activity-filter">
      <div className="flex flex-wrap gap-2" role="group" aria-label={t("hiveActivity.filter.kinds")}>
        {KIND_GROUPS.map((g) => {
          const on = filter.kinds.includes(g);
          return <button key={g} type="button" className={on ? "btn btn-sm" : "btn btn-secondary btn-sm"} aria-pressed={on} onClick={() => toggle(g)}>{t(`hiveActivity.groups.${g}`)}</button>;
        })}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <input type="search" className="input input-sm sm:!w-72" aria-label={t("hiveActivity.filter.search")} placeholder={t("hiveActivity.filter.search")} value={filter.q} onChange={(e) => onChange({ ...filter, q: e.target.value })} />
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={filter.cloud} onChange={(e) => onChange({ ...filter, cloud: e.target.checked })} />
          {t("hiveActivity.filter.cloud", { count: cloud })}
        </label>
      </div>
    </div>
  );
}

function ActionRow({ a, me, cameras }: { a: HiveAction; me: string; cameras: HiveCamera[] }) {
  const { t } = useTranslation();
  const { label, target, actor } = describe(t, a, me, cameras);
  const details = detailsText(a.details);
  return (
    <li className="py-3 text-sm flex flex-wrap justify-between gap-2" data-testid="hive-action" data-action={a.action}>
      <span>
        <span className="font-medium">{label}</span>
        {target && <span className="muted"> · {target}</span>}
        {details && <span className="muted text-xs block break-all">{details}</span>}
      </span>
      <span className="muted whitespace-nowrap">{a.at ? new Date(a.at).toLocaleString() : ""} · {actor}</span>
    </li>
  );
}
