import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { unwrap } from "@camhive/api";
import { api } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
import { HiveNav } from "@/components/HiveNav";
import { useRecordingStop } from "@/components/RecordingStopped";
import { Trans, useTranslation } from "react-i18next";
import { useMe } from "@/lib/session";
import { useHives } from "@/lib/hives";
import { useTransport } from "@/lib/useTransport";
import { lanBase, relayBase, type Decision } from "@/lib/transport";
import { useGrant } from "@/lib/useGrant";
import { useCameras, useHiveClient, useLocalLoginStatus, useStorage, useUpdateStatus } from "@/lib/hiveApi";
import { useArchiveEncryption } from "@/lib/archiveEncryption";
import { emergencyOffers } from "@/lib/emergency";
import { hiveCapability, useHiveCapability, type Capability } from "@/lib/capabilities";
import {
  GROUPS, answered, connectionHints, footageFacts, localLoginFacts, moveFacts, stripItems, updatesFacts, versionFact,
  type Answer, type Group, type StripItem,
} from "@/lib/hiveSettings";
import { DeleteRecordings } from "@/components/DeleteRecordings";
import { LocalLoginSettings } from "@/components/LocalLogin";
import { NeedsUpdate } from "@/components/NeedsUpdate";
import { UpdatesSettings } from "@/components/Updates";
import { EncryptionSettings } from "@/components/ArchiveEncryption";
import { StorageSettings } from "@/components/Storage";
import { DiskHealthRow, MoveDataSettings, useMove } from "@/components/DiskHealth";
import { LanAddressStep } from "@/components/LanAddress";
import { Facts, PanelRow, SettingsGroup, SettingsRow, groupAnchor } from "@/components/SettingsLayout";
import type { Hive } from "@/lib/hives";

/** The hive's settings (#174): a status strip, then the groups in a fixed order. */
export function HiveSettings() {
  const { t } = useTranslation();
  const { id } = useParams({ from: "/hives/$id/settings" });
  const me = useMe();
  const hives = useHives();
  const hive = hives.data ? [...hives.data.hives, ...hives.data.shared].find((h) => h.id === id) : undefined;
  if (me.isPending || hives.isPending) return <p className="muted">…</p>;
  if (!me.data) return <p className="card">{t("errors.unauthenticated")}</p>;
  if (!hive) return <p className="card">{t("errors.not_found")}</p>;
  return <SettingsPage hive={hive} owned={!!hives.data?.hives.some((h) => h.id === id)} />;
}

type Query<T> = { data?: T; isLoading: boolean; isError: boolean };

/**
 * Reads what the strip and the rows show as the page opens: one session
 * with the hive (the one the hive's other pages share), and only what the
 * hive announces (ADR-0022) and the viewer may read.
 */
function useSettingsState(hive: Hive, owner: boolean) {
  const { i18n } = useTranslation();
  const g = useGrant(hive);
  const client = useHiveClient(hive, g.grant);
  const has = (c: Capability) => hiveCapability(hive, c).has;
  const live = hive.status !== "revoked";
  const storage = useStorage(hive, client, live && has("storage"));
  const encryption = useArchiveEncryption(hive, owner ? client : null);
  const updates = useUpdateStatus(hive, client, owner && has("updates"));
  const localLogin = useLocalLoginStatus(hive, client, owner && has("local_login"));
  const move = useMove(hive, client, owner && has("disk.health"));
  const { decision, probing, reprobe } = useTransport(hive);
  const waiting = g.access.isLoading;
  const offline = !hive.online && !client; // the cloud says so, and no local way in: nothing to ask
  const toAnswer = <T,>(q: Query<T>): Answer<T> => ({ data: q.data, loading: waiting || q.isLoading, offline });
  const queries: Query<unknown>[] = [storage, encryption, updates, localLogin, move];
  const storageA = toAnswer(storage);
  const stop = useRecordingStop(hive, client); // #172
  const strip = stripItems({ hive, owner, decision, lang: i18n.language, storage: storageA, encryption: toAnswer(encryption), updates: toAnswer(updates), stop });
  const of = (id: StripItem["id"]) => strip.find((i) => i.id === id)?.facts ?? [];
  return {
    g, decision, probing, reprobe, strip,
    /** Every answer is in (or will not come): the page has its final height. */
    settled: !waiting && !queries.some((q) => q.isLoading),
    noAnswer: queries.some((q) => q.isError),
    health: storage.data?.disk?.health,
    localLoginEnabled: localLogin.data?.enabled,
    rows: {
      storage: answered(hive, "storage", storageA, (o) => footageFacts(o, i18n.language)),
      disk: of("disk"),
      encryption: of("encryption"),
      move: answered(hive, "disk.health", toAnswer(move), moveFacts),
      localLogin: answered(hive, "local_login", toAnswer(localLogin), localLoginFacts),
      updates: answered(hive, "updates", toAnswer(updates), updatesFacts),
    },
  };
}

function SettingsPage({ hive, owned }: { hive: Hive; owned: boolean }) {
  const { t } = useTranslation();
  const live = hive.status !== "revoked";
  const owner = owned && live;
  const s = useSettingsState(hive, owner);
  const offers = emergencyOffers(s.g, owned, s.decision, hive);
  useScrollToHash(s.settled);
  const groups: Partial<Record<Group, ReactNode>> = {
    connection: (
      <SettingsGroup group="connection">
        <ConnectionRows hive={hive} decision={s.decision} probing={s.probing} reprobe={s.reprobe} offerAddress={offers.address} />
      </SettingsGroup>
    ),
    recordings: live && (
      <SettingsGroup group="recordings">
        <StorageSettings hive={hive} owner={owned} state={s.rows.storage} />
        {owner && <DiskHealthRow hive={hive} state={s.rows.disk} health={s.health} />}
        {owner && <EncryptionSettings hive={hive} state={s.rows.encryption} />}
        {owner && <DeleteRow hive={hive} />}
        {owner && <MoveDataSettings hive={hive} state={s.rows.move} />}
      </SettingsGroup>
    ),
    access: owner && (
      <SettingsGroup group="access">
        <LocalLoginSettings hive={hive} state={s.rows.localLogin} enabled={s.localLoginEnabled} />
      </SettingsGroup>
    ),
    software: (
      <SettingsGroup group="software">
        {owner && <UpdatesSettings hive={hive} state={s.rows.updates} />}
        <SettingsRow id="version" title={t("settings.version")} state={<Facts facts={[versionFact(hive.version)]} />} />
        <SettingsRow id="hive-id" title={t("settings.hiveId")} state={<span className="font-mono">{hive.id}</span>} />
      </SettingsGroup>
    ),
    danger: owner && (
      <SettingsGroup group="danger" danger>
        <RemoveRow hive={hive} />
      </SettingsGroup>
    ),
  };
  const shown = GROUPS.filter((g) => groups[g]);
  return (
    <div className="space-y-6 max-w-none">
      <HiveNav hive={hive} active="settings" />
      <StatusStrip items={s.strip} />
      {s.noAnswer && <p className="muted text-sm" data-testid="hive-no-answer">{t("settings.noAnswer")}</p>}
      {s.g.access.error && <p role="alert" className="error text-sm">{errorMessage(t, s.g.access.error)}</p>}
      <div className="lg:grid lg:grid-cols-[11rem_minmax(0,1fr)] lg:gap-10">
        <nav aria-label={t("settings.index")} className="hidden lg:block">
          <ul className="sticky top-20 space-y-2 text-sm">
            {shown.map((g) => <li key={g}><a href={`#${groupAnchor(g)}`} className="muted hover:text-[var(--fg)]">{t(`settings.group.${g}`)}</a></li>)}
          </ul>
        </nav>
        <div className="space-y-8 max-w-3xl min-w-0">
          {shown.map((g) => <div key={g}>{groups[g]}</div>)}
        </div>
      </div>
    </div>
  );
}

/**
 * A link to a group or row (the disk banner's to the move) lands on it once
 * the page has its answers: the router scrolls before the rows exist on a
 * cold load, and rows above the target grow as their states arrive.
 */
function useScrollToHash(settled: boolean) {
  const hash = useRouterState({ select: (r) => r.location.hash }).replace(/^#/, "");
  const done = useRef("");
  useEffect(() => {
    if (!settled || !hash || done.current === hash) return;
    done.current = hash;
    document.getElementById(hash)?.scrollIntoView?.();
  }, [settled, hash]);
}

/** One line, no buttons: what the owner looks for first (#174). */
function StatusStrip({ items }: { items: StripItem[] }) {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="settings-strip-title">
      <h2 id="settings-strip-title" className="sr-only">{t("settings.strip")}</h2>
      <dl className="card !py-3 !px-4 flex flex-wrap gap-x-6 gap-y-1.5 text-sm" data-testid="settings-strip">
        {items.map((i) => (
          <div key={i.id} className="flex min-w-0 gap-1.5" data-testid={`strip-${i.id}`}>
            <dt className="muted shrink-0">{t(`settings.label.${i.id}`)}</dt>
            <dd className="font-medium min-w-0 [overflow-wrap:anywhere]"><Facts facts={i.facts} /></dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

type ConnectionProps = { hive: Hive; decision: Decision | null; probing: boolean; reprobe: () => Promise<void>; offerAddress: boolean };

/** Which path the app uses and why (ADR-0012), the callouts that apply, the facts behind it. */
function ConnectionRows({ hive, decision, probing, reprobe, offerAddress }: ConnectionProps) {
  const { t } = useTranslation();
  const hints = connectionHints(hive, decision, navigator.userAgent);
  const path = decision ? `transport.path.${decision.path}` : "transport.probing";
  return (
    <>
      <SettingsRow id="path" testId="transport" title={t("settings.pathTitle")}
        state={<><span className="font-medium text-[var(--fg)]" data-testid="path">{t(path)}</span>{decision && <>{". "}{t(`transport.reason.${decision.reason}`)}</>}</>}
        action={<button type="button" className="btn btn-secondary btn-sm" disabled={probing} onClick={() => void reprobe()}>{probing ? t("transport.probing") : t("transport.reprobe")}</button>}>
        {(hints.rebind || hints.chrome || offerAddress) && (
          <div className="space-y-3">
            {hints.rebind && (
              <div className="callout text-sm" data-testid="rebind-hint">
                <p className="font-medium">{t("transport.rebindTitle")}</p>
                <p className="muted mt-1">{t("transport.rebindHelp")}</p>
                <code className="block mt-2 text-xs">h.camhive.app</code>
              </div>
            )}
            {hints.chrome && <p className="callout text-sm" data-testid="lna-hint">{t("transport.lnaNote")}</p>}
            {offerAddress && <LanAddressStep hive={hive} probing={probing} reprobe={reprobe} bare />}
          </div>
        )}
      </SettingsRow>
      <Disclosure title={t("settings.details")} testId="connection-details">
        <dl className="kv text-sm space-y-2">
          <div><dt className="muted">{t("transport.lanName")}</dt><dd>{lanBase(hive) ?? t("transport.none")}</dd></div>
          {!!hive.lan?.addrs?.length && <div><dt className="muted">{t("transport.lanAddrs")}</dt><dd>{hive.lan.addrs.join(", ")}</dd></div>}
          {hive.lan?.cert_issuer && <div><dt className="muted">{t("transport.cert")}</dt><dd>{t(`transport.issuer.${hive.lan.cert_issuer}`, { defaultValue: hive.lan.cert_issuer })}</dd></div>}
          <div><dt className="muted">{t("transport.relay")}</dt><dd>{relayBase(hive) ?? t("transport.none")}</dd></div>
        </dl>
      </Disclosure>
      <Disclosure title={t("settings.videoTitle")}>
        <p className="muted text-sm">{t("transport.liveNote")}</p>
      </Disclosure>
    </>
  );
}

function Disclosure({ title, testId, children }: { title: string; testId?: string; children: ReactNode }) {
  return (
    <details className="py-4 group" data-testid={testId}>
      <summary className="cursor-pointer font-medium list-none flex items-center gap-2">
        <span aria-hidden="true" className="muted transition-transform group-open:rotate-90">›</span>{title}
      </summary>
      <div className="mt-3">{children}</div>
    </details>
  );
}

function DeleteRow({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const canDelete = useHiveCapability(hive, "archive.delete");
  return (
    <PanelRow id="delete-recordings" title={t("deletion.title")} help={t("deletion.help")} openLabel={t("deletion.open")} disabled={!hive.online || !canDelete.has}
      notice={!canDelete.has && <NeedsUpdate hive={hive} since={canDelete.since} owner />} panel={() => <HiveDeletion hive={hive} />} />
  );
}

function RemoveRow({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  async function remove() {
    if (!window.confirm(t("transport.confirmRemove", { name: hive.name }))) return;
    setError(null);
    try {
      unwrap(await api.DELETE("/v1/hives/{id}", { params: { path: { id: hive.id } } }));
      await qc.invalidateQueries({ queryKey: ["hives"] });
      await nav({ to: "/" });
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  return (
    <SettingsRow id="remove" title={t("transport.removeTitle")} state={<Trans i18nKey="transport.removeHelp" components={{ code: <code /> }} />}
      action={<button type="button" className="btn btn-danger btn-sm" onClick={() => void remove()}>{t("transport.remove")}</button>}>
      {error && <p role="alert" className="error text-sm">{error}</p>}
    </SettingsRow>
  );
}

/**
 * The deletion form with the hive's cameras, once the row is opened.
 */
function HiveDeletion({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const { grant, access } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const cameras = useCameras(hive, client);
  if (access.error) return <p role="alert" className="error text-sm">{errorMessage(t, access.error)}</p>;
  if (!client || !cameras.data) return <p className="muted text-sm">…</p>;
  return <DeleteRecordings client={client} cameras={cameras.data} hiveName={hive.name ?? hive.id} />;
}

