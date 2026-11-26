import { useMemo, useState } from "react";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { CamhiveError, type HiveCamera } from "@camhive/api";
import { useMe } from "@/lib/session";
import { useHives } from "@/lib/hives";
import { useGrant } from "@/lib/useGrant";
import { useCameraHealth, useCameras, useHiveClient } from "@/lib/hiveApi";
import { useSessionAlive } from "@/lib/eviction";
import { apiSignalling, hiveSignalling, type PeerFactory } from "@/lib/live";
import { streamFor } from "@/lib/reconnect";
import { dayKey } from "@/lib/archive";
import { atHome, transport } from "@/lib/transport";
import { emergencyOffers } from "@/lib/emergency";
import { useTransport } from "@/lib/useTransport";
import { COLUMNS, LAYOUTS, readLayout, writeLayout, type Layout } from "@/lib/layout";
import type { PlayerDeps } from "@/lib/archivePlayer";
import { errorMessage } from "@/lib/errors";
import { LiveTile } from "@/components/LiveTile";
import { HiveNav } from "@/components/HiveNav";
import { CameraDetail } from "@/components/CameraDetail";
import { AddCamera, CameraRow, groupSorted } from "@/components/CameraManage";
import { LocalSessionBanner, LocalSignIn } from "@/components/LocalLogin";
import { LanAddressStep } from "@/components/LanAddress";
import { ArchiveLockNotice } from "@/components/ArchiveEncryption";
import { DiskHealthNotice } from "@/components/DiskHealth";
import { RecordingStoppedNotice } from "@/components/RecordingStopped";
import { presenceOf } from "@/lib/archiveAutolock";
import { useReportPresence } from "@/lib/usePresence";

/** `view: "recordings"` opens the camera at its timeline instead of live (#84). */
export type HiveSearch = { camera?: string; day?: string; view?: "recordings" };

/**
 * One hive: every camera live in a grid (or a list without video), the opened
 * camera on top with its recordings on a timeline, and camera management for
 * the owner. Replaces the separate live, archive and cameras pages.
 */
export function HivePage({ peer, playerDeps }: { peer?: PeerFactory; playerDeps?: PlayerDeps } = {}) {
  const { t } = useTranslation();
  const { id } = useParams({ from: "/hives/$id" });
  const search = useSearch({ from: "/hives/$id" });
  const nav = useNavigate();
  const me = useMe();
  const hives = useHives();
  const hive = hives.data ? [...hives.data.hives, ...hives.data.shared].find((h) => h.id === id) : undefined;
  const { grant, local, localSession, canSignInLocally, access } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const cameras = useCameras(hive, client);
  const health = useCameraHealth(hive, client);
  const { alive } = useSessionAlive(hive?.id, local ? undefined : grant?.session_id);
  const { decision, probing, reprobe } = useTransport(hive);
  const home = atHome(local, decision);
  useReportPresence(hive, client, presenceOf(local, decision, probing), grant?.role === "owner"); // #147: the archive may lock when the owner leaves
  const [layout, setLayout] = useState<Layout>(() => readLayout());
  const [adding, setAdding] = useState(false);
  const signal = useMemo(() => (home && client ? hiveSignalling(client) : hive ? apiSignalling(hive.id) : null), [hive?.id, home, client]); // eslint-disable-line react-hooks/exhaustive-deps

  if (me.isPending || hives.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  if (!hive) return <p className="card">{t("errors.not_found")}</p>;

  const owner = grant?.role === "owner";
  const offers = emergencyOffers({ grant, local, localSession, canSignInLocally }, !!hives.data?.hives.some((h) => h.id === hive.id), decision, hive);
  const quality = home ? "full" : grant?.quality; // the plan limits remote viewing only
  const evicted = alive === false;
  // Owners also see switched-off cameras, so they can switch them back on.
  const all = groupSorted((cameras.data ?? []).filter((c) => owner || c.enabled));
  const openId = search.camera ?? (search.day ? all.find((c) => c.enabled)?.id : undefined);
  const open = all.find((c) => c.id === openId);
  const others = all.filter((c) => c !== open);
  const day = search.day ?? dayKey(new Date());
  const go = (s: HiveSearch) => void nav({ to: "/hives/$id", params: { id: hive.id }, search: s });
  const openCamera = (c: HiveCamera) => go({ camera: c.id });
  const openRecordings = (c: HiveCamera) => go({ camera: c.id, view: "recordings" });
  const pick = (l: Layout) => { setLayout(l); writeLayout(l); };
  const healthOf = (c: HiveCamera) => health.data?.find((h) => h.camera === c.id);

  return (
    <div className="space-y-5 max-w-none">
      <HiveNav hive={hive} active="cameras" actions={
        <div className="flex items-center gap-3 text-sm">
          {grant && <span className="muted" data-testid="quality">{t(`live.quality.${quality ?? "full"}`)}</span>}
          <LayoutSwitch value={layout} onChange={pick} />
        </div>
      } />

      {local && !localSession && <p role="status" className="card muted" data-testid="local-mode">{t("live.localMode")}</p>}
      {localSession && <LocalSessionBanner hive={hive} />}
      {offers.signIn && decision && <LocalSignIn hive={hive} base={transport.baseFor(hive, decision)} />}
      {offers.address && <LanAddressStep hive={hive} probing={probing} reprobe={reprobe} />}
      <RecordingStoppedNotice hive={hive} client={client} />
      {!open && <ArchiveLockNotice hive={hive} client={client} owner={owner} />}
      {!open && <DiskHealthNotice hive={hive} client={client} owner={owner} />}
      {!hive.online && !local && <p role="alert" className="card muted">{t("live.hiveOffline")}</p>}
      {evicted && <p role="alert" className="card error" data-testid="evicted">{t("live.evicted")}</p>}
      {access.isError && !evicted && <p role="alert" className="card error">{errorMessage(t, access.error)}</p>}
      {cameras.isError && <p role="alert" className="card error">{cameras.error instanceof CamhiveError ? errorMessage(t, cameras.error) : t(localSession ? "localLogin.unreachable" : "errors.hive_unreachable")}</p>}
      {cameras.data && all.length === 0 && !owner && <p className="card muted">{t("live.noCameras")}</p>}
      {cameras.isPending && <p className="card muted" role="status" data-testid="cameras-loading">{t("cameras.loading")}</p>}

      {open && grant && signal && (
        <CameraDetail key={open.id} hive={hive} grant={grant} quality={quality} client={client} camera={open} health={healthOf(open)} signal={signal}
          day={day} recordings={search.view === "recordings"} onDay={(d) => go({ camera: open.id, day: d === dayKey(new Date()) ? undefined : d })} onClose={() => go({})}
          paused={evicted} peer={peer} playerDeps={playerDeps} />
      )}

      {adding && client && <AddCamera client={client} onDone={() => setAdding(false)} />}

      {signal && grant && cameras.data && (others.length > 0 || owner) && (
        <section aria-label={t("cameras.title")} className={open ? "pt-2" : ""}>
          {open && others.length > 0 && <h2 className="eyebrow mb-2">{t("live.otherCameras")}</h2>}
          {layout === "list" ? (
            <ul className="card !py-1 divide-y divide-[var(--border)]" data-testid="grid">
              {others.map((c) => <CameraRow key={c.id} hive={hive} camera={c} online={healthOf(c)?.online} health={healthOf(c)} client={client} editable={owner} onOpen={() => openCamera(c)} onRecordings={() => openRecordings(c)} />)}
              {owner && client && !adding && (
                <li className="py-3"><button type="button" className="link text-sm" onClick={() => setAdding(true)}><span aria-hidden="true">+ </span>{t("cameras.add")}</button></li>
              )}
            </ul>
          ) : (
            <div className={`grid ${COLUMNS[layout]} gap-2`} data-testid="grid">
              {others.map((c) => (
                <LiveTile key={c.id} hiveId={hive.id} camera={c} health={healthOf(c)} stream={streamFor(quality, layout)} signal={signal} iceServers={grant.ice_servers} paused={evicted} peer={peer} onOpen={() => openCamera(c)} onRecordings={() => openRecordings(c)} />
              ))}
              {owner && client && !adding && (
                <button type="button" className="aspect-video rounded-lg border-2 border-dashed flex items-center justify-center gap-1.5 text-sm muted hover:text-[var(--fg)] transition-colors" style={{ borderColor: "var(--border-strong)" }} onClick={() => setAdding(true)}>
                  <span aria-hidden="true" className="text-lg leading-none">+</span>{t("cameras.add")}
                </button>
              )}
            </div>
          )}
        </section>
      )}
    </div>
  );
}

const ICONS: Record<Layout, React.ReactNode> = {
  large: <><rect x="3" y="3" width="18" height="8" rx="1.5" /><rect x="3" y="13" width="18" height="8" rx="1.5" /></>,
  medium: <><rect x="3" y="3" width="8" height="8" rx="1.5" /><rect x="13" y="3" width="8" height="8" rx="1.5" /><rect x="3" y="13" width="8" height="8" rx="1.5" /><rect x="13" y="13" width="8" height="8" rx="1.5" /></>,
  small: <>{[3, 10, 17].flatMap((y) => [3, 10, 17].map((x) => <rect key={`${x}${y}`} x={x} y={y} width="4" height="4" rx=".8" />))}</>,
  list: <><path d="M8 6h13M8 12h13M8 18h13" /><circle cx="4" cy="6" r="1" /><circle cx="4" cy="12" r="1" /><circle cx="4" cy="18" r="1" /></>,
};

/** Tile size switch drawn as little grids; the list shows the cameras without video. */
function LayoutSwitch({ value, onChange }: { value: Layout; onChange: (l: Layout) => void }) {
  const { t } = useTranslation();
  return (
    <div role="group" aria-label={t("live.layout")} className="seg">
      {LAYOUTS.map((l) => (
        <button key={l} type="button" aria-pressed={l === value} aria-label={t(`live.layouts.${l}`)} title={t(`live.layouts.${l}`)} onClick={() => onChange(l)} className="!px-2">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true">{ICONS[l]}</svg>
        </button>
      ))}
    </div>
  );
}
