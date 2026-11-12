import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CameraHealth, HiveCamera, ICEServer } from "@camhive/api";
import { LiveSession, type LiveState, type PeerFactory, type Signalling } from "@/lib/live";
import { backoffMs } from "@/lib/reconnect";
import { useOnScreen } from "@/lib/useOnScreen";

export interface LiveTileProps {
  camera: HiveCamera;
  health?: CameraHealth;
  stream: "main" | "sub";
  signal: Signalling;
  iceServers?: ICEServer[];
  /** Stops the session (eviction, page leaving). */
  paused?: boolean;
  peer?: PeerFactory;
  children?: React.ReactNode; // PTZ controls etc.
  /** Makes the picture a button that opens this camera. */
  onOpen?: () => void;
}

/**
 * One camera: video element, WebRTC session with reconnect, status line.
 * A tile scrolled out of view stops its session, so a long grid only streams what is on screen.
 */
export function LiveTile({ camera, health, stream, signal, iceServers, paused, peer, children, onOpen }: LiveTileProps) {
  const { t } = useTranslation();
  const video = useRef<HTMLVideoElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<LiveState | "idle">("idle");
  const [attempt, setAttempt] = useState(0);
  const offline = health ? !health.online : false;
  const visible = useOnScreen(box);

  useEffect(() => {
    if (paused || offline || !camera.enabled || !visible) {
      setState("idle");
      return;
    }
    let session: LiveSession | null = null;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const retry = () => {
      if (cancelled) return;
      timer = setTimeout(() => setAttempt((a) => a + 1), backoffMs(attempt));
    };
    setState("connecting");
    LiveSession.start({
      signal, camera: camera.id, stream, iceServers, peer,
      onTrack: (ms) => { if (video.current) video.current.srcObject = ms; },
      onState: (s) => {
        if (cancelled) return;
        setState(s);
        if (s === "connected") setAttempt(0);
        if (s === "failed") retry();
      },
    }).then((s) => {
      if (cancelled) void s.stop();
      else session = s;
    }).catch(() => {
      if (cancelled) return;
      setState("failed");
      retry();
    });
    return () => {
      cancelled = true;
      clearTimeout(timer);
      void session?.stop();
      if (video.current) video.current.srcObject = null;
    };
  }, [camera.id, camera.enabled, stream, signal, iceServers, paused, offline, visible, attempt, peer]);

  const status = !camera.enabled ? t("live.disabled") : offline ? t("live.cameraOffline") : state === "connected" ? t("live.connected") : state === "failed" ? t("live.reconnecting") : state === "connecting" ? t("live.connecting") : "";
  const kbps = health?.streams?.[stream]?.bitrate_kbps;
  const live = state === "connected";
  return (
    <div ref={box} className="group relative bg-black rounded-lg overflow-hidden aspect-video" data-testid="tile" data-camera={camera.id} data-state={state} data-stream={stream}>
      <video ref={video} className="w-full h-full object-contain" autoPlay muted playsInline />
      {onOpen && <button type="button" className="absolute inset-0 cursor-pointer" aria-label={t("live.open", { name: camera.name })} onClick={onOpen} />}
      {(state === "connecting" || state === "failed") && (
        <div className="absolute inset-0 flex items-center justify-center pointer-events-none" aria-hidden="true">
          <span className="h-8 w-8 rounded-full border-2 border-white/15 border-t-white/70 animate-spin" />
        </div>
      )}
      <div className="pointer-events-none absolute top-0 left-0 right-0 flex items-center justify-between gap-2 px-2.5 pt-2 pb-6 text-xs text-white" style={{ background: "linear-gradient(rgba(0,0,0,.55), transparent)" }}>
        <span className="flex min-w-0 items-center gap-2">
          {live && <span className="dot" style={{ color: "var(--accent)" }} aria-hidden="true" />}
          <span className="font-medium truncate">{camera.name}</span>
        </span>
        <span className="flex items-center gap-2">
          {kbps !== undefined && live && <span className="opacity-70 tabular-nums">{kbps} kbit/s</span>}
          <span data-testid="tile-status" className={live ? "sr-only" : "opacity-90"}>{status}</span>
          <button type="button" className="pointer-events-auto glass glass-btn !h-7 !w-7 opacity-80 group-hover:opacity-100" aria-label={t("live.fullscreen", { name: camera.name })} onClick={() => void box.current?.requestFullscreen?.()}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" /></svg>
          </button>
        </span>
      </div>
      {children}
    </div>
  );
}

