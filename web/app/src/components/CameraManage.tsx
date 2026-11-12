import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { CamhiveError, unwrapHive, type HiveCamera, type HiveClient, type hiveComponents } from "@camhive/api";
import { errorMessage } from "@/lib/errors";
import { Field } from "@/components/Field";

type Discovered = hiveComponents["schemas"]["Discovered"];
type Probe = hiveComponents["schemas"]["ProbeResult"];
type Creds = { username: string; password: string };

/*
 * Camera management for the hive owner (#53): add via ONVIF discovery or
 * manual RTSP, edit, switch on and off, remove. Used by the hive page.
 */

export function groupSorted(cs: HiveCamera[]): HiveCamera[] {
  return [...cs].sort((a, b) => (a.group ?? "").localeCompare(b.group ?? "") || a.name.localeCompare(b.name));
}

/** Edit, switch and remove one camera; errors are kept for the caller to show. */
export function useCameraActions(camera: HiveCamera, client: HiveClient | null, onRemoved?: () => void) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // ADR-0017: a change of address the hive refused because another device answers there.
  const [mismatch, setMismatch] = useState<hiveComponents["schemas"]["CameraPatch"] | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["hive"] });
  async function patch(body: hiveComponents["schemas"]["CameraPatch"]) {
    if (!client) return;
    setError(null);
    setMismatch(null);
    try {
      unwrapHive(await client.PATCH("/v0/cameras/{id}", { params: { path: { id: camera.id } }, body }));
      await refresh();
      setEditing(false);
    } catch (err) {
      if (err instanceof CamhiveError && err.code === "camera_mismatch") setMismatch(body);
      setError(errorMessage(t, err));
    }
  }
  /** The owner says the device at the new address is a replacement camera. */
  const replace = () => (mismatch ? patch({ ...mismatch, replace_identity: true }) : Promise.resolve());
  async function remove() {
    if (!client || !window.confirm(t("cameras.confirmRemove", { name: camera.name }))) return;
    try {
      unwrapHive(await client.DELETE("/v0/cameras/{id}", { params: { path: { id: camera.id } } }));
      onRemoved?.();
      await refresh();
    } catch (err) {
      setError(errorMessage(t, err));
    }
  }
  return { editing, setEditing, error, mismatch: !!mismatch, replace, patch, remove };
}

/** One camera in the list layout: name (opens it), source, health; management for the owner. */
export function CameraRow({ camera, online, client, editable, onOpen }: { camera: HiveCamera; online: boolean | undefined; client: HiveClient | null; editable: boolean; onOpen: () => void }) {
  const { t } = useTranslation();
  const { editing, setEditing, error, mismatch, replace, patch, remove } = useCameraActions(camera, client);
  const main = camera.streams?.main;
  return (
    <li className="py-3" data-testid="camera" data-camera={camera.id}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="font-medium"><button type="button" className="hover:underline underline-offset-4 text-left" aria-label={t("live.open", { name: camera.name })} onClick={onOpen}>{camera.name}</button> {camera.group && <span className="muted text-sm">· {camera.group}</span>}</p>
          <p className="text-xs muted">{camera.source.kind} · {camera.source.address}{main?.Codec ? ` · ${main.Codec}` : ""}{main?.Width ? ` ${main.Width}×${main.Height}` : ""}</p>
        </div>
        <div className="flex items-center gap-3 text-sm">
          <span className={!camera.enabled || !online ? "badge" : "badge badge-success"} data-testid="camera-status"><span className="dot" aria-hidden="true" />{!camera.enabled ? t("live.disabled") : online === undefined ? "…" : online ? t("hives.statusOnline") : t("hives.statusOffline")}</span>
          {editable && (
            <>
              <label className="flex items-center gap-1"><input type="checkbox" checked={camera.enabled} onChange={(e) => void patch({ enabled: e.target.checked })} aria-label={t("cameras.enabledFor", { name: camera.name })} />{t("cameras.enabled")}</label>
              <button type="button" className="link" onClick={() => setEditing((v) => !v)}>{t("cameras.edit")}</button>
              <button type="button" className="link link-danger" onClick={() => void remove()}>{t("cameras.remove")}</button>
            </>
          )}
        </div>
      </div>
      {error && <p role="alert" className="error text-sm mt-2">{error}</p>}
      {mismatch && <ReplaceButton onReplace={replace} />}
      {editing && <CameraEditForm camera={camera} onSubmit={patch} />}
    </li>
  );
}

export function CameraEditForm({ camera, onSubmit }: { camera: HiveCamera; onSubmit: (p: hiveComponents["schemas"]["CameraPatch"]) => Promise<void> }) {
  const { t } = useTranslation();
  const [name, setName] = useState(camera.name);
  const [group, setGroup] = useState(camera.group ?? "");
  const [creds, setCreds] = useState<Creds>({ username: camera.credentials?.username ?? "", password: "" });
  const [recordAudio, setRecordAudio] = useState(camera.record_audio ?? false);
  const [host, setHost] = useState(hostOf(camera.source.address));
  function submit(e: FormEvent) {
    e.preventDefault();
    const p: hiveComponents["schemas"]["CameraPatch"] = { name, group, record_audio: recordAudio };
    if (creds.password) p.credentials = creds;
    if (host.trim() && host.trim() !== hostOf(camera.source.address)) p.host = host.trim();
    void onSubmit(p);
  }
  return (
    <form className="mt-3 grid gap-x-4 sm:grid-cols-2" onSubmit={submit} aria-label={t("cameras.editForm", { name: camera.name })}>
      <Field label={t("cameras.name")} name={`name-${camera.id}`} required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} />
      <Field label={t("cameras.group")} name={`group-${camera.id}`} maxLength={100} value={group} onChange={(e) => setGroup(e.target.value)} />
      <Field label={t("cameras.username")} name={`user-${camera.id}`} autoComplete="off" value={creds.username} onChange={(e) => setCreds({ ...creds, username: e.target.value })} />
      <Field label={t("cameras.password")} name={`pass-${camera.id}`} type="password" autoComplete="new-password" hint={t("cameras.passwordKeep")} value={creds.password} onChange={(e) => setCreds({ ...creds, password: e.target.value })} />
      <Field label={t("cameras.host")} name={`host-${camera.id}`} autoComplete="off" spellCheck={false} value={host} onChange={(e) => setHost(e.target.value)} />
      <CameraIdentity camera={camera} />
      <label className="sm:col-span-2 flex items-start gap-2 text-sm mb-3">
        <input type="checkbox" className="mt-1" checked={recordAudio} onChange={(e) => setRecordAudio(e.target.checked)} aria-describedby={`audio-hint-${camera.id}`} />
        <span>{t("cameras.recordAudio")}<span id={`audio-hint-${camera.id}`} className="block muted text-xs">{t("cameras.recordAudioHint")}</span></span>
      </label>
      <div><button className="btn">{t("cameras.save")}</button></div>
    </form>
  );
}

export function AddCamera({ client, onDone }: { client: HiveClient; onDone: () => void }) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"discover" | "rtsp">("discover");
  return (
    <section className="card space-y-3" aria-label={t("cameras.add")}>
      <div className="flex items-center justify-between gap-3">
      <div role="tablist" className="seg">
        {(["discover", "rtsp"] as const).map((m) => (
          <button key={m} role="tab" type="button" aria-selected={m === mode} onClick={() => setMode(m)}>{t(`cameras.mode.${m}`)}</button>
        ))}
      </div>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onDone}>{t("cameras.cancel")}</button>
      </div>
      {mode === "discover" ? <DiscoverFlow client={client} onDone={onDone} /> : <RtspForm client={client} onDone={onDone} />}
    </section>
  );
}

function useCreate(client: HiveClient, onDone: () => void) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const create = async (body: hiveComponents["schemas"]["CameraCreate"]) => {
    setError(null);
    try {
      unwrapHive(await client.POST("/v0/cameras", { body }));
      await qc.invalidateQueries({ queryKey: ["hive"] });
      onDone();
    } catch (err) {
      setError(err instanceof CamhiveError && err.code === "invalid" ? t("cameras.invalid", { message: err.message }) : errorMessage(t, err));
    }
  };
  return { create, error };
}

function RtspForm({ client, onDone }: { client: HiveClient; onDone: () => void }) {
  const { t } = useTranslation();
  const { create, error } = useCreate(client, onDone);
  const [name, setName] = useState("");
  const [group, setGroup] = useState("");
  const [url, setUrl] = useState("");
  const [creds, setCreds] = useState<Creds>({ username: "", password: "" });
  function submit(e: FormEvent) {
    e.preventDefault();
    void create({ name, group, source: { kind: "rtsp", address: url }, ...(creds.username ? { credentials: creds } : {}) });
  }
  return (
    <form className="grid gap-x-4 sm:grid-cols-2" onSubmit={submit} aria-label={t("cameras.mode.rtsp")}>
      <Field label={t("cameras.name")} name="rtsp-name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} />
      <Field label={t("cameras.group")} name="rtsp-group" maxLength={100} value={group} onChange={(e) => setGroup(e.target.value)} />
      <div className="sm:col-span-2"><Field label={t("cameras.rtspUrl")} name="rtsp-url" required placeholder="rtsp://192.168.1.50:554/stream1" hint={t("cameras.rtspHint")} value={url} onChange={(e) => setUrl(e.target.value)} /></div>
      <Field label={t("cameras.username")} name="rtsp-user" autoComplete="off" value={creds.username} onChange={(e) => setCreds({ ...creds, username: e.target.value })} />
      <Field label={t("cameras.password")} name="rtsp-pass" type="password" autoComplete="new-password" value={creds.password} onChange={(e) => setCreds({ ...creds, password: e.target.value })} />
      {error && <p role="alert" className="error text-sm sm:col-span-2">{error}</p>}
      <div><button className="btn">{t("cameras.create")}</button></div>
    </form>
  );
}

function DiscoverFlow({ client, onDone }: { client: HiveClient; onDone: () => void }) {
  const { t } = useTranslation();
  const { create, error: createError } = useCreate(client, onDone);
  const [found, setFound] = useState<Discovered[] | null>(null);
  const [scanning, setScanning] = useState(false);
  const [picked, setPicked] = useState<Discovered | null>(null);
  const [manualAddr, setManualAddr] = useState("");
  const [creds, setCreds] = useState<Creds>({ username: "", password: "" });
  const [probe, setProbe] = useState<Probe | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [group, setGroup] = useState("");
  const [main, setMain] = useState("");
  const [sub, setSub] = useState("");

  async function scan() {
    setScanning(true);
    setError(null);
    try {
      setFound(unwrapHive(await client.GET("/v0/discovery")));
    } catch (err) {
      setError(errorMessage(t, err));
    } finally {
      setScanning(false);
    }
  }
  async function doProbe(e: FormEvent) {
    e.preventDefault();
    const address = picked?.xaddr ?? manualAddr;
    setError(null);
    setProbe(null);
    try {
      const p = unwrapHive(await client.POST("/v0/discovery/probe", { body: { address, ...(creds.username ? { credentials: creds } : {}) } }));
      setProbe(p);
      setName(picked?.name || p.device?.Model || "");
      const sorted = [...p.profiles].sort((a, b) => b.width * b.height - a.width * a.height);
      setMain(sorted[0]?.token ?? "");
      setSub(sorted.length > 1 ? sorted[sorted.length - 1]!.token : "");
    } catch (err) {
      setError(err instanceof CamhiveError && err.code === "camera_auth" ? t("cameras.cameraAuth") : errorMessage(t, err));
    }
  }
  function add(e: FormEvent) {
    e.preventDefault();
    if (!probe) return;
    const prof = (tok: string) => probe.profiles.find((p) => p.token === tok);
    const streams: Record<string, hiveComponents["schemas"]["StreamInput"]> = {};
    const m = prof(main);
    if (m) streams.main = { url: m.url, profile_token: m.token, codec: m.encoding, width: m.width, height: m.height };
    const s = sub && sub !== main ? prof(sub) : undefined;
    if (s) streams.sub = { url: s.url, profile_token: s.token, codec: s.encoding, width: s.width, height: s.height };
    void create({ name, group, source: { kind: "onvif", address: picked?.xaddr ?? manualAddr }, ...(creds.username ? { credentials: creds } : {}), streams });
  }
  const profileLabel = (p: Probe["profiles"][number]) => `${p.name || p.token} · ${p.encoding} ${p.width}×${p.height}${p.fps ? ` @${p.fps}` : ""}`;
  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <button type="button" className="btn btn-secondary btn-sm" onClick={() => void scan()} disabled={scanning}>{scanning ? t("cameras.scanning") : t("cameras.scan")}</button>
        <span className="muted text-sm">{t("cameras.scanHint")}</span>
      </div>
      {found && found.length === 0 && <p className="muted text-sm">{t("cameras.noneFound")}</p>}
      {found && found.length > 0 && (
        <ul className="space-y-1 text-sm" aria-label={t("cameras.found")}>
          {found.map((d) => (
            <li key={d.xaddr}>
              <button type="button" className={d.xaddr === picked?.xaddr ? "font-semibold" : "link"} onClick={() => { setPicked(d); setProbe(null); }}>{d.name || d.xaddr}</button> <span className="muted">{d.xaddr}</span>
            </li>
          ))}
        </ul>
      )}
      <form className="grid gap-x-4 sm:grid-cols-3" onSubmit={(e) => void doProbe(e)} aria-label={t("cameras.probeForm")}>
        <Field label={t("cameras.address")} name="onvif-addr" required={!picked} placeholder="http://192.168.1.50/onvif/device_service" value={picked?.xaddr ?? manualAddr} onChange={(e) => { setPicked(null); setManualAddr(e.target.value); }} />
        <Field label={t("cameras.username")} name="onvif-user" autoComplete="off" value={creds.username} onChange={(e) => setCreds({ ...creds, username: e.target.value })} />
        <Field label={t("cameras.password")} name="onvif-pass" type="password" autoComplete="new-password" value={creds.password} onChange={(e) => setCreds({ ...creds, password: e.target.value })} />
        <div><button className="btn">{t("cameras.probe")}</button></div>
      </form>
      {error && <p role="alert" className="error text-sm">{error}</p>}
      {probe && (
        <form className="grid gap-x-4 sm:grid-cols-2" onSubmit={add} aria-label={t("cameras.addForm")} data-testid="probe-result">
          <p className="sm:col-span-2 text-sm muted">{[probe.device?.Manufacturer, probe.device?.Model, probe.device?.FirmwareVersion].filter(Boolean).join(" · ")}</p>
          <Field label={t("cameras.name")} name="onvif-name" required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} />
          <Field label={t("cameras.group")} name="onvif-group" maxLength={100} value={group} onChange={(e) => setGroup(e.target.value)} />
          <div className="mb-4">
            <label className="block mb-1 text-sm font-medium" htmlFor="main">{t("cameras.mainStream")}</label>
            <select id="main" className="input" value={main} onChange={(e) => setMain(e.target.value)}>{probe.profiles.map((p) => <option key={p.token} value={p.token}>{profileLabel(p)}</option>)}</select>
          </div>
          <div className="mb-4">
            <label className="block mb-1 text-sm font-medium" htmlFor="sub">{t("cameras.subStream")}</label>
            <select id="sub" className="input" value={sub} onChange={(e) => setSub(e.target.value)}><option value="">{t("cameras.noSub")}</option>{probe.profiles.map((p) => <option key={p.token} value={p.token}>{profileLabel(p)}</option>)}</select>
          </div>
          {createError && <p role="alert" className="error text-sm sm:col-span-2">{createError}</p>}
          <div><button className="btn">{t("cameras.create")}</button></div>
        </form>
      )}
    </div>
  );
}

/** The host of a camera address (IP or name), or "" when it cannot be read. */
export function hostOf(address: string): string {
  try {
    return new URL(address).hostname.replace(/^\[|\]$/g, "");
  } catch {
    return "";
  }
}

/** What the hive knows of the camera and whether it can find it again (ADR-0017). */
function CameraIdentity({ camera }: { camera: HiveCamera }) {
  const { t } = useTranslation();
  const id = camera.identity ?? {};
  const parts = [[id.manufacturer, id.model].filter(Boolean).join(" "), id.serial && t("cameras.serial", { serial: id.serial }), id.mac && t("cameras.mac", { mac: id.mac })].filter(Boolean);
  return (
    <div className="text-xs muted mb-3 space-y-1 self-end">
      {parts.length > 0 && <p data-testid="camera-identity">{parts.join(" · ")}</p>}
      {camera.network && <p data-testid="camera-network">{t(`cameras.network.${camera.network}`)}</p>}
    </div>
  );
}

/** Confirms that the device at the new address is a replacement camera. */
export function ReplaceButton({ onReplace }: { onReplace: () => Promise<void> }) {
  const { t } = useTranslation();
  return <button type="button" className="btn btn-secondary btn-sm mt-2" onClick={() => void onReplace()}>{t("cameras.replaceCamera")}</button>;
}
