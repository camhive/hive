import { useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { unwrap, type AuditEvent } from "@camhive/api";
import { api, API_URL } from "@/lib/api";
import { useMe } from "@/lib/session";
import { useHives } from "@/lib/hives";
import { errorMessage } from "@/lib/errors";

const PAGE = 50;

/** The account's audit trail with cursor paging, optionally one hive's, and the GDPR export (#55). */
export function Activity() {
  const { t } = useTranslation();
  const me = useMe();
  const hives = useHives();
  const [hive, setHive] = useState<string>("");
  const [exportError, setExportError] = useState<string | null>(null);
  const q = useInfiniteQuery({
    queryKey: ["activity", hive],
    initialPageParam: undefined as number | undefined,
    queryFn: async ({ pageParam }) => {
      const query = { before: pageParam, limit: PAGE };
      return hive
        ? unwrap(await api.GET("/v1/hives/{id}/activity", { params: { path: { id: hive }, query } }))
        : unwrap(await api.GET("/v1/me/activity", { params: { query } }));
    },
    getNextPageParam: (last) => last.next_before ?? undefined,
    enabled: !!me.data,
  });
  if (me.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  const events = q.data?.pages.flatMap((p) => p.events) ?? [];

  async function download() {
    setExportError(null);
    try {
      const res = await fetch(`${API_URL}/v1/me/export`, { credentials: "include" });
      if (!res.ok) throw new Error(String(res.status));
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement("a");
      a.href = url;
      a.download = `camhive-export-${new Date().toISOString().slice(0, 10)}.json`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 10_000);
    } catch {
      setExportError(t("activity.exportFailed"));
    }
  }

  return (
    <div className="space-y-4 max-w-none">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="page-title">{t("activity.title")}</h1>
        <div className="flex items-center gap-3 text-sm">
          <label className="muted" htmlFor="hive-filter">{t("activity.scope")}</label>
          <select id="hive-filter" className="input input-sm !w-auto" value={hive} onChange={(e) => setHive(e.target.value)}>
            <option value="">{t("activity.account")}</option>
            {hives.data?.hives.map((h) => <option key={h.id} value={h.id}>{h.name}</option>)}
          </select>
          <button type="button" className="link" onClick={() => void download()}>{t("activity.export")}</button>
        </div>
      </div>
      {exportError && <p role="alert" className="error text-sm">{exportError}</p>}
      {q.isError && <p role="alert" className="card error">{errorMessage(t, q.error)}</p>}
      {q.data && events.length === 0 && <p className="card muted">{t("activity.empty")}</p>}
      {events.length > 0 && (
        <ul className="card !py-2 divide-y divide-[var(--border)]" aria-label={t("activity.title")}>
          {events.map((e) => <EventRow key={e.id} e={e} />)}
        </ul>
      )}
      {q.hasNextPage && <button type="button" className="btn" disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>{t("activity.more")}</button>}
    </div>
  );
}

function EventRow({ e }: { e: AuditEvent }) {
  const { t } = useTranslation();
  const label = t(`activity.actions.${e.action}`, { defaultValue: e.action });
  const details = e.details && Object.keys(e.details).length ? JSON.stringify(e.details) : "";
  return (
    <li className="py-3 text-sm flex flex-wrap justify-between gap-2" data-testid="event" data-action={e.action}>
      <span>
        <span className="font-medium">{label}</span>
        {e.target_id && <span className="muted"> · {e.target_type ? `${e.target_type} ` : ""}{e.target_id}</span>}
        {details && <span className="muted text-xs block break-all">{details}</span>}
      </span>
      <span className="muted whitespace-nowrap">{new Date(e.at).toLocaleString()} · {t(`activity.actors.${e.actor_type}`, { defaultValue: e.actor_type })}</span>
    </li>
  );
}
