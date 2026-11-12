import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { CamhiveError, unwrapHive, type AccessGrant, type CameraHealth, type HiveCamera, type HiveClient } from "@camhive/api";
import type { Hive } from "@/lib/hives";
import { transport } from "@/lib/transport";
import { errorMessage } from "@/lib/errors";
import { streamFor } from "@/lib/reconnect";
import type { PeerFactory, Signalling } from "@/lib/live";
import { activityRuns, coverage, dayKey, dayStart, exportCapMinutes, nextRecorded, recordedAt, useCalendar, useEvents, useExports, useTimeline, type ExportJob, type StreamName } from "@/lib/archive";
import { ArchivePlayer, DEFAULT_WINDOW_S, type PlayerDeps } from "@/lib/archivePlayer";
import { ArchiveSound } from "@/lib/archiveSound";
import { LiveTile } from "@/components/LiveTile";
import { PtzPad } from "@/components/PtzPad";
import { Timeline } from "@/components/Timeline";
import { Field } from "@/components/Field";
import { CameraEditForm, useCameraActions } from "@/components/CameraManage";

export type CameraDetailProps = {
  hive: Hive;
  grant: AccessGrant;
  /** Live quality for this viewer: the plan's when remote, full at home. */
  quality?: "sub" | "full";
  client: HiveClient | null;
  camera: HiveCamera;
  health?: CameraHealth;
  signal: Signalling;
  /** The day on the timeline; a past day opens the recording instead of live video. */
  day: string;
  onDay: (day: string) => void;
  onClose: () => void;
  paused?: boolean;
  peer?: PeerFactory;
  playerDeps?: PlayerDeps;
};

/**
 * The opened camera: live by default, with the day's recordings on a timeline
 * underneath. Seeking on the timeline plays the recording from there;
 * "Back to live" returns. Owners edit, switch off and remove the camera here.
 */
export function CameraDetail({ hive, grant, quality, client, camera, health, signal, day, onDay, onClose, paused, peer, playerDeps }: CameraDetailProps) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const today = dayKey(new Date());
  const [mode, setMode] = useState<"live" | "archive">(day === today ? "live" : "archive");
  const [stream, setStream] = useState<StreamName>("main");
  const calendar = useCalendar(client, camera.id, stream);
  const timeline = useTimeline(client, camera.id, stream, day);
  const events = useEvents(client, camera.id, day);
  const ranges = useMemo(() => coverage(timeline.data ?? [], dayStart(day)), [timeline.data, day]);
  const activity = useMemo(() => activityRuns(timeline.data ?? [], dayStart(day)), [timeline.data, day]);
  const [position, setPosition] = useState<Date | null>(null);
  const [playError, setPlayError] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);
  const video = useRef<HTMLVideoElement>(null);
  const player = useRef<ArchivePlayer | null>(null);
  const audioEl = useRef<HTMLAudioElement>(null);
  const sound = useRef<ArchiveSound | null>(null);
  const playing = useRef<{ at: Date; seconds: number } | null>(null); // the window the player shows
  const [soundOn, setSoundOn] = useState(false);
  const owner = grant.role === "owner";
  const operator = owner || grant.role === "operator";
  const actions = useCameraActions(camera, client, onClose);

  useEffect(() => () => { player.current?.stop(); sound.current?.stop(); }, []);
  // Another camera or day: start from a clean player.
  useEffect(() => {
    player.current?.stop();
    sound.current?.stop();
    playing.current = null;
    setPosition(null);
    setPlayError(null);
    setExporting(false);
  }, [camera.id, day]);

  async function seek(want: Date) {
    setMode("archive");
    const at = nextRecorded(ranges, want);
    if (!at) {
      setPosition(want);
      setPlayError(t("archive.noFootageHere"));
      return;
    }
    setPosition(at);
    setPlayError(null);
    if (!video.current) return;
    player.current ??= new ArchivePlayer(video.current, (path, sig) => transport.fetch(hive, path, { token: grant.token, signal: sig }),
      { onError: () => setPlayError(t("archive.playbackFailed")), ...playerDeps });
    const range = recordedAt(ranges, at);
    const duration = Math.min(DEFAULT_WINDOW_S, Math.max(1, Math.floor(((range?.[1] ?? at.getTime() + DEFAULT_WINDOW_S * 1000) - at.getTime()) / 1000)));
    try {
      await player.current.play(camera.id, stream, at, duration, { main: camera.streams?.main?.Codec, sub: camera.streams?.sub?.Codec });
      playing.current = { at, seconds: duration };
      if (soundOn) void startSound();
    } catch (err) {
      setPlayError(err instanceof CamhiveError ? errorMessage(t, err) : t("archive.playbackFailed"));
    }
  }

  // Sound is fetched only while the viewer wants it (ADR-0015).
  async function startSound() {
    const w = playing.current;
    if (!w || !audioEl.current || !video.current) return;
    sound.current ??= new ArchiveSound(audioEl.current, video.current, (path, sig) => transport.fetch(hive, path, { token: grant.token, signal: sig }));
    try {
      await sound.current.start(camera.id, stream, w.at, w.seconds);
    } catch (err) {
      setSoundOn(false);
      setPlayError(errorMessage(t, err));
    }
  }

  function toggleSound() {
    const on = !soundOn;
    setSoundOn(on);
    if (on) void startSound();
    else sound.current?.stop();
  }

  function backToLive() {
    player.current?.stop();
    sound.current?.stop();
    playing.current = null;
    setPosition(null);
    setPlayError(null);
    setExporting(false);
    setMode("live");
    if (day !== today) onDay(today);
  }

  function pickDay(d: string) {
    if (d !== today) setMode("archive");
    onDay(d);
  }

  const live = mode === "live";
  const days = calendar.data ?? [];
  return (
    <section className="space-y-3" aria-label={camera.name} data-testid="camera-detail">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold truncate">{camera.name}{camera.group && <span className="muted font-normal text-sm"> · {camera.group}</span>}</h2>
        <div className="flex items-center gap-1">
          {owner && client && (
            <>
              <button type="button" className="btn btn-ghost btn-sm" aria-expanded={actions.editing} onClick={() => actions.setEditing((v) => !v)}>{t("cameras.editCamera")}</button>
              <button type="button" className="btn btn-ghost btn-sm" onClick={() => void actions.patch({ enabled: !camera.enabled })}>{camera.enabled ? t("cameras.turnOff") : t("cameras.turnOn")}</button>
              <button type="button" className="btn btn-ghost btn-sm !text-[var(--danger)]" onClick={() => void actions.remove()}>{t("cameras.remove")}</button>
            </>
          )}
          <button type="button" className="btn btn-ghost btn-sm" aria-label={t("live.close")} onClick={onClose}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg>
          </button>
        </div>
      </div>
      {actions.error && <p role="alert" className="error text-sm">{actions.error}</p>}
      {actions.editing && <div className="card"><CameraEditForm camera={camera} onSubmit={actions.patch} /></div>}

      {live && (
        <LiveTile camera={camera} health={health} stream={streamFor(quality, "detail")} signal={signal} iceServers={grant.ice_servers} paused={paused} peer={peer}>
          {operator && client && <PtzPad client={client} cameraId={camera.id} />}
        </LiveTile>
      )}
      <div className={live ? "hidden" : "bg-black rounded-lg aspect-video overflow-hidden relative"}>
        <video ref={video} className="w-full h-full object-contain" controls playsInline data-testid="player"
          onError={(e) => { if (e.currentTarget.getAttribute("src")) setPlayError(t("archive.playbackFailed")); }} />
        <audio ref={audioEl} hidden />
        {position && <span className="absolute top-2 left-2 text-xs glass px-2 py-0.5 rounded" data-testid="position">{position.toLocaleString()}</span>}
      </div>
      {playError && <p role="alert" className="error text-sm">{playError}</p>}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="seg flex-wrap" data-testid="calendar" role="group" aria-label={t("archive.calendar")}>
          {days.map((d) => <button key={d} type="button" aria-pressed={d === day} onClick={() => pickDay(d)}>{d}</button>)}
        </div>
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <label className="muted" htmlFor="stream">{t("archive.stream")}</label>
          <select id="stream" className="input input-sm !w-auto" value={stream} onChange={(e) => setStream(e.target.value as StreamName)}>
            <option value="main">main</option>
            <option value="sub">sub</option>
          </select>
          {!live && <button type="button" className="btn btn-secondary btn-sm" aria-pressed={soundOn} onClick={toggleSound}>{soundOn ? t("archive.soundOn") : t("archive.soundOff")}</button>}
          {operator && client && <button type="button" className="btn btn-secondary btn-sm" onClick={() => setExporting((v) => !v)} disabled={!position}>{t("archive.exportFromHere")}</button>}
          {!live && <button type="button" className="btn btn-sm" onClick={backToLive}><span className="dot" style={{ color: "var(--accent)" }} aria-hidden="true" />{t("live.backToLive")}</button>}
        </div>
      </div>
      {calendar.data && days.length === 0 && <p className="muted text-sm">{t("archive.noFootage")}</p>}
      <Timeline day={day} ranges={ranges} events={(events.data ?? []).map((e) => Date.parse(e.at ?? ""))} position={live ? (day === today ? new Date() : null) : position} onSeek={(at) => void seek(at)} activity={activity} />

      {operator && client && (
        <div aria-label={t("archive.export")} role="region">
          {exporting && position && (
            <ExportForm capMinutes={exportCapMinutes(grant.export_max_minutes)} start={position} onSubmit={async (minutes) => {
              unwrapHive(await client.POST("/v0/cameras/{id}/exports", { params: { path: { id: camera.id } }, body: { start: position.toISOString(), duration_seconds: minutes * 60, stream } }));
              setExporting(false);
              await qc.invalidateQueries({ queryKey: ["exports"] });
            }} />
          )}
          <ExportList client={client} hive={hive} token={grant.token} />
        </div>
      )}
    </section>
  );
}

function ExportForm({ capMinutes, start, onSubmit }: { capMinutes: number; start: Date; onSubmit: (minutes: number) => Promise<void> }) {
  const { t } = useTranslation();
  const [minutes, setMinutes] = useState(Math.min(10, capMinutes));
  const [error, setError] = useState<string | null>(null);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      await onSubmit(minutes);
    } catch (err) {
      setError(err instanceof CamhiveError && err.code === "plan_limit" ? t("archive.exportLimit", { count: capMinutes }) : errorMessage(t, err));
    }
  }
  return (
    <form className="card mt-1 flex flex-wrap items-end gap-3" onSubmit={(e) => void submit(e)} aria-label={t("archive.exportForm")}>
      <p className="text-sm muted w-full">{t("archive.exportStart", { at: start.toLocaleString() })}</p>
      <div className="w-40"><Field label={t("archive.exportMinutes")} name="minutes" type="number" min={1} max={capMinutes} required value={minutes} onChange={(e) => setMinutes(Number(e.target.value))} hint={t("archive.exportCap", { count: capMinutes })} /></div>
      <button className="btn mb-4">{t("archive.startExport")}</button>
      {error && <p role="alert" className="error text-sm w-full">{error}</p>}
    </form>
  );
}

function ExportList({ client, hive, token }: { client: HiveClient; hive: Hive; token: string }) {
  const { t } = useTranslation();
  const jobs = useExports(client);
  async function download(j: ExportJob) {
    const res = await transport.fetch(hive, `/v0/exports/${encodeURIComponent(j.id ?? "")}/file`, { token });
    if (!res.ok) return;
    const url = URL.createObjectURL(await res.blob());
    const a = document.createElement("a");
    a.href = url;
    a.download = j.file_name ?? "export.mp4";
    a.click();
    setTimeout(() => URL.revokeObjectURL(url), 10_000);
  }
  if (!jobs.data?.length) return null;
  return (
    <div className="mt-2">
      <h3 className="eyebrow mb-1">{t("archive.exports")}</h3>
      <ul className="text-sm divide-y divide-[var(--border)]" aria-label={t("archive.exports")}>
        {jobs.data.map((j) => (
          <li key={j.id} className="flex items-center justify-between gap-3 py-2" data-testid="export" data-status={j.status}>
            <span>{j.start ? new Date(j.start).toLocaleString() : ""} · {Math.round((j.duration_seconds ?? 0) / 60)} min · {j.stream}</span>
            <span className="flex items-center gap-2">
              <span className="muted">{t(`archive.status.${j.status ?? "running"}`)}{j.error ? `: ${j.error}` : ""}</span>
              {j.status === "done" && <button type="button" className="link" onClick={() => void download(j)}>{t("archive.download")}</button>}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}
