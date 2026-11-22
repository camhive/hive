import { useEffect, useReducer, useRef, useState, type Dispatch, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { CamhiveError, type HiveClient } from "@camhive/api";
import type { Hive } from "@/lib/hives";
import { useGrant } from "@/lib/useGrant";
import { useLocalSession } from "@/lib/localLogin";
import { readOffline } from "@/lib/offline";
import { useHiveClient } from "@/lib/hiveApi";
import { useHiveCapability } from "@/lib/capabilities";
import { errorMessage } from "@/lib/errors";
import { saveBlob } from "@/lib/download";
import {
  START, enableStep, encryptionActions, encryptionError, encryptionKey, keyFileName, passphraseProblem, useArchiveEncryption,
  type EnableEvent, type EnableFlow, type Encryption, type EncryptionActions,
} from "@/lib/archiveEncryption";
import { AFTER_CHOICES, AWAY_CHOICES, locksIn, presenceOf, type AutolockSettings } from "@/lib/archiveAutolock";
import { useReportPresence } from "@/lib/usePresence";
import { useTransport } from "@/lib/useTransport";
import { NeedsUpdate } from "@/components/NeedsUpdate";
import { Field } from "@/components/Field";

/**
 * The owner's encryption settings (#93, ADR-0019): switch on, unlock and
 * lock, the key file and the passphrase. It asks the hive for access only
 * once opened: a remote session counts against the plan.
 */
export function EncryptionSettings({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const cap = useHiveCapability(hive, "archive.encryption");
  const [open, setOpen] = useState(false);
  // The emergency case is a hive the cloud cannot reach with a local session (or offline grant) on the LAN.
  const reachable = hive.online || !!useLocalSession(hive.id) || !!readOffline(hive.id);
  return (
    <section className="card max-w-3xl" aria-labelledby="encryption-title">
      <h2 id="encryption-title" className="text-lg font-semibold mb-1">{t("encryption.title")}</h2>
      <p className="muted text-sm mb-3">{t("encryption.help")}</p>
      {!cap.has ? <NeedsUpdate hive={hive} since={cap.since} owner />
        : open ? <EncryptionPanel hive={hive} />
        : <button type="button" className="btn btn-secondary" disabled={!reachable} onClick={() => setOpen(true)}>{t("encryption.open")}</button>}
    </section>
  );
}

function EncryptionPanel({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const { grant, access, local, localSession } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const enc = useArchiveEncryption(hive, client);
  const { decision, probing } = useTransport(hive);
  useReportPresence(hive, client, presenceOf(local, decision, probing), true); // #147: only the owner opens this panel
  // Above every condition: a failed refetch must not reset the enable flow (ADR-0019 §4, the key file).
  const [flow, dispatch] = useReducer(enableStep, START);
  const err = access.error ?? enc.error;
  const problem = err ? <p role="alert" className="error text-sm mb-3">{errorMessage(t, err)}</p> : null;
  if (!client || !enc.data) return problem ?? <p className="muted text-sm">…</p>;
  return (
    <>
      {problem}
      <EncryptionOwner hive={hive} enc={enc.data} actions={encryptionActions(client)} cloud={!localSession} flow={flow} dispatch={dispatch} />
    </>
  );
}

type OwnerProps = { hive: Hive; enc: Encryption; actions: EncryptionActions; cloud: boolean; flow: EnableFlow; dispatch: Dispatch<EnableEvent> };

/**
 * Off: the enable flow, which stays on screen until the key file is saved.
 * On: the state, unlock or lock, and with a session through Camhive the
 * key file and the passphrase (a local session gets 403 there).
 */
export function EncryptionOwner({ hive, enc, actions, cloud, flow, dispatch }: OwnerProps) {
  const { t } = useTranslation();
  const enabling = flow.step === "choose" || flow.step === "backup";
  if (enc.state === "off" || enabling) {
    return cloud ? <EnableEncryption hive={hive} flow={flow} dispatch={dispatch} actions={actions} /> : <p className="callout text-sm">{t("encryption.cloudOnly")}</p>;
  }
  return (
    <div className="space-y-4 text-sm">
      <p className="font-medium" data-testid="encryption-state">{t(`encryption.state.${enc.state}`)}</p>
      {!enc.sealing && <p role="alert" className="callout" data-testid="not-sealing">{t("encryption.notSealing")}</p>}
      <LockControl hive={hive} enc={enc} actions={actions} />
      {cloud && <AutolockControl hive={hive} enc={enc} actions={actions} />}
      {cloud ? (
        <>
          <KeyFileButton hive={hive} actions={actions} />
          <ChangePassphrase hive={hive} actions={actions} />
        </>
      ) : <p className="muted">{t("encryption.cloudOnly")}</p>}
    </div>
  );
}

/** Keeps the shared answer in step with what the hive said, for the hive page too. */
function useStore(hive: Hive) {
  const qc = useQueryClient();
  return (e: Encryption) => qc.setQueryData(encryptionKey(hive.id), e);
}

/** One request at a time with its error text. */
function useAttempt() {
  const { t } = useTranslation();
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function attempt(f: () => Promise<void>) {
    setError(null);
    setBusy(true);
    try {
      await f();
    } catch (err) {
      setError(encryptionError(t, err));
    } finally {
      setBusy(false);
    }
  }
  return { error, setError, busy, attempt };
}

/**
 * The credential a password manager files the passphrase under: one per
 * hive, never the Camhive account, so the account password is neither
 * filled into an unlock (a wasted strike) nor overwritten by a passphrase.
 */
function PassphraseAccount({ hive }: { hive: Hive }) {
  return <input type="text" name="username" autoComplete="username" value={`camhive-archive-${hive.id}`} readOnly hidden />;
}

function Problem({ error }: { error: string | null }) {
  return error ? <p role="alert" className="error text-sm mb-3">{error}</p> : null;
}

type FlowProps = { hive: Hive; flow: EnableFlow; dispatch: Dispatch<EnableEvent>; actions: EncryptionActions };

/** ADR-0019 §4: the warning, the passphrase twice, then the key file right away. */
function EnableEncryption({ hive, flow, dispatch, actions }: FlowProps) {
  const { t } = useTranslation();
  const store = useStore(hive);
  const qc = useQueryClient();
  // On already (another device, or a retry after a lost answer): the key file is still due.
  async function enable(passphrase: string) {
    try {
      store(await actions.enable(passphrase));
    } catch (err) {
      if (!(err instanceof CamhiveError && err.code === "encryption_on")) throw err;
      void qc.invalidateQueries({ queryKey: encryptionKey(hive.id) });
    }
    dispatch("enabled");
  }
  if (flow.step === "warn" || flow.step === "done") {
    return (
      <div className="space-y-3 text-sm">
        <p className="font-medium" data-testid="encryption-state">{t("encryption.state.off")}</p>
        <div role="note" className="callout">
          <p className="font-medium">{t("encryption.warnTitle")}</p>
          <p className="mt-1">{t("encryption.warn")}</p>
        </div>
        <button type="button" className="btn btn-secondary" onClick={() => dispatch("understood")}>{t("encryption.understood")}</button>
      </div>
    );
  }
  if (flow.step === "choose") return <ChoosePassphrase hive={hive} enable={enable} />;
  return <Backup hive={hive} saved={flow.saved} dispatch={dispatch} actions={actions} />;
}

/** The key file right after switching on; focus moves here so the step is not missed. */
function Backup({ hive, saved, dispatch, actions }: { hive: Hive; saved: boolean; dispatch: Dispatch<EnableEvent>; actions: EncryptionActions }) {
  const { t } = useTranslation();
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => heading.current?.focus(), []);
  return (
    <div className="space-y-3 text-sm" data-testid="encryption-backup">
      <h3 ref={heading} tabIndex={-1} className="font-medium outline-none">{t("encryption.backupTitle")}</h3>
      <p>{t("encryption.backup")}</p>
      <KeyFileButton hive={hive} actions={actions} onSaved={() => dispatch("saved")} />
      <button type="button" className="btn" disabled={!saved} onClick={() => dispatch("finish")}>{t("encryption.finish")}</button>
    </div>
  );
}

function ChoosePassphrase({ hive, enable }: { hive: Hive; enable: (passphrase: string) => Promise<void> }) {
  const { t } = useTranslation();
  const [passphrase, setPassphrase] = useState("");
  const [again, setAgain] = useState("");
  const { error, setError, busy, attempt } = useAttempt();
  function submit(e: FormEvent) {
    e.preventDefault();
    const problem = passphraseProblem(passphrase, again);
    if (problem) setError(t(`encryption.${problem}`));
    else void attempt(() => enable(passphrase));
  }
  return (
    <form onSubmit={submit} aria-label={t("encryption.chooseTitle")}>
      <PassphraseAccount hive={hive} />
      <Field label={t("encryption.passphrase")} name="enc-passphrase" type="password" autoComplete="new-password" required hint={t("encryption.passphraseHint")} value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
      <Field label={t("encryption.passphraseAgain")} name="enc-passphrase-again" type="password" autoComplete="new-password" required value={again} onChange={(e) => setAgain(e.target.value)} />
      <Problem error={error} />
      <button className="btn" disabled={busy}>{t("encryption.enable")}</button>
    </form>
  );
}

/** Downloads the key file, the owner's backup; useless without the passphrase. */
function KeyFileButton({ hive, actions, onSaved, label }: { hive: Hive; actions: EncryptionActions; onSaved?: () => void; label?: string }) {
  const { t } = useTranslation();
  const { error, busy, attempt } = useAttempt();
  const [saved, setSaved] = useState(false);
  const download = () => void attempt(async () => {
    saveBlob(await actions.keyFile(), keyFileName(hive.id));
    setSaved(true);
    onSaved?.();
  });
  return (
    <div>
      <Problem error={error} />
      <button type="button" className="btn btn-secondary" disabled={busy} onClick={download}>{label ?? t("encryption.downloadKeyFile")}</button>
      {saved && <span role="status" className="ml-3">{t("encryption.saved")}</span>}
    </div>
  );
}

/** Locked: the passphrase; unlocked: lock again. */
function LockControl({ hive, enc, actions }: { hive: Hive; enc: Encryption; actions: EncryptionActions }) {
  const { t } = useTranslation();
  const store = useStore(hive);
  const { error, busy, attempt } = useAttempt();
  if (enc.state === "locked") return <UnlockForm hive={hive} actions={actions} />;
  return (
    <div>
      <Problem error={error} />
      <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => void attempt(async () => { store(await actions.lock()); })}>{t("encryption.lock")}</button>
    </div>
  );
}

/** Re-renders every `ms`, for a countdown that reads the clock as it renders. */
function useTick(ms: number): void {
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), ms);
    return () => clearInterval(id);
  }, [ms]);
}

/**
 * The automatic lock (#147): after inactivity, and when the owner leaves
 * home; with a hive that has it and a session through Camhive.
 */
function AutolockControl({ hive, enc, actions }: { hive: Hive; enc: Encryption; actions: EncryptionActions }) {
  const { t } = useTranslation();
  const cap = useHiveCapability(hive, "archive.autolock");
  const store = useStore(hive);
  const { error, busy, attempt } = useAttempt();
  useTick(30_000);
  const a = enc.autolock;
  if (!cap.has || !a) return null;
  const set = (patch: AutolockSettings) => void attempt(async () => { store(await actions.autolock(patch)); });
  const due = a.locks_at && enc.state === "unlocked" ? locksIn(a.locks_at) : null;
  return (
    <fieldset className="space-y-3" data-testid="autolock">
      <legend className="font-medium mb-1">{t("encryption.autolockTitle")}</legend>
      <p className="muted">{t("encryption.autolockHelp")}</p>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block"><span className="block mb-1">{t("encryption.autolockAfter")}</span>
          <select className="input" value={a.after} disabled={busy} onChange={(e) => set({ after: e.target.value as AutolockSettings["after"] })}>
            {AFTER_CHOICES.map((c) => <option key={c} value={c}>{t(`encryption.autolockAfterChoice.${c}`)}</option>)}
          </select>
        </label>
        <label className="block"><span className="block mb-1">{t("encryption.autolockAway")}</span>
          <select className="input" value={a.away} disabled={busy} onChange={(e) => set({ away: e.target.value as AutolockSettings["away"] })}>
            {AWAY_CHOICES.map((c) => <option key={c} value={c}>{t(`encryption.autolockAwayChoice.${c}`)}</option>)}
          </select>
        </label>
      </div>
      {a.away !== "off" && <p className="muted text-xs">{t("encryption.autolockAwayNote")}</p>}
      {due && <p role="status" data-testid="locks-in">{t(`encryption.autolockLocksIn.${a.reason ?? "inactivity"}.${due.unit}`, { count: due.count })}</p>}
      <Problem error={error} />
    </fieldset>
  );
}

function UnlockForm({ hive, actions, onUnlocked }: { hive: Hive; actions: EncryptionActions; onUnlocked?: () => void }) {
  const { t } = useTranslation();
  const store = useStore(hive);
  const [passphrase, setPassphrase] = useState("");
  const { error, busy, attempt } = useAttempt();
  function submit(e: FormEvent) {
    e.preventDefault();
    void attempt(async () => {
      store(await actions.unlock(passphrase));
      onUnlocked?.();
    });
  }
  return (
    <form onSubmit={submit} aria-label={t("encryption.unlockTitle")}>
      <PassphraseAccount hive={hive} />
      <Field label={t("encryption.passphrase")} name={`unlock-${hive.id}`} type="password" autoComplete="current-password" required autoFocus value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
      <Problem error={error} />
      <button className="btn" disabled={busy}>{t("encryption.unlock")}</button>
    </form>
  );
}

function ChangePassphrase({ hive, actions }: { hive: Hive; actions: EncryptionActions }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [changed, setChanged] = useState(false);
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const { error, setError, busy, attempt } = useAttempt();
  function submit(e: FormEvent) {
    e.preventDefault();
    const problem = passphraseProblem(next, again);
    if (problem) setError(t(`encryption.${problem}`));
    else void attempt(async () => {
      await actions.change(current, next);
      setCurrent(""); setNext(""); setAgain(""); // no passphrase lingers in memory
      setChanged(true);
      setOpen(false);
    });
  }
  if (changed) {
    return (
      <div className="space-y-2" data-testid="passphrase-changed">
        <p role="status">{t("encryption.changed")}</p>
        <KeyFileButton hive={hive} actions={actions} label={t("encryption.downloadNewKeyFile")} />
      </div>
    );
  }
  if (!open) return <button type="button" className="btn btn-secondary" onClick={() => setOpen(true)}>{t("encryption.change")}</button>;
  return (
    <form onSubmit={submit} aria-label={t("encryption.change")}>
      <PassphraseAccount hive={hive} />
      <Field label={t("encryption.current")} name="enc-current" type="password" autoComplete="current-password" required value={current} onChange={(e) => setCurrent(e.target.value)} />
      <Field label={t("encryption.newPassphrase")} name="enc-new" type="password" autoComplete="new-password" required hint={t("encryption.passphraseHint")} value={next} onChange={(e) => setNext(e.target.value)} />
      <Field label={t("encryption.newAgain")} name="enc-new-again" type="password" autoComplete="new-password" required value={again} onChange={(e) => setAgain(e.target.value)} />
      <Problem error={error} />
      <button className="btn" disabled={busy}>{t("encryption.changeSubmit")}</button>
    </form>
  );
}

/**
 * Where footage would be when the archive is locked (after every restart
 * of the hive): the owner unlocks in one tap, a member learns who can.
 * Nothing while it is off or unlocked, or on a hive without encryption.
 */
export function ArchiveLockNotice({ hive, client, owner, onUnlocked }: { hive: Hive; client: HiveClient | null; owner: boolean; onUnlocked?: () => void }) {
  const { t } = useTranslation();
  const enc = useArchiveEncryption(hive, client);
  const [open, setOpen] = useState(false);
  if (enc.data?.state !== "locked") return null;
  return (
    <section aria-labelledby={`archive-locked-${hive.id}`} className="card callout text-sm space-y-2" data-testid="archive-locked">
      <p id={`archive-locked-${hive.id}`}>{t(owner ? "encryption.lockedOwner" : "encryption.lockedMember")}</p>
      {owner && !enc.data.sealing && <p data-testid="not-sealing">{t("encryption.notSealing")}</p>}
      {owner && client && (open ? <UnlockForm hive={hive} actions={encryptionActions(client)} onUnlocked={onUnlocked} />
        : <button type="button" className="btn min-h-11" onClick={() => setOpen(true)}>{t("encryption.unlock")}</button>)}
    </section>
  );
}
