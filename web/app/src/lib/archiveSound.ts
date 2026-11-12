// Plays an archive span's recorded sound next to its video (ADR-0015). The
// video is video-only (browsers refuse the camera's audio format inside
// MP4), so the hive answers GET …/archive/audio with WAV a minute at a time,
// and this keeps an <audio> element in step with the <video>.
import { CamhiveError } from "@camhive/api";
import type { ByteSource } from "./archivePlayer";

/** Seconds of sound per request; the hive allows up to 60. */
export const SOUND_CHUNK_S = 60;

/** Drift between sound and picture tolerated before the sound is moved. */
const MAX_DRIFT_S = 0.3;

export function audioPath(camera: string, stream: string, start: Date, durationS: number): string {
  const q = new URLSearchParams({ stream, start: start.toISOString(), duration: String(Math.max(1, Math.floor(durationS))) });
  return `/v0/cameras/${encodeURIComponent(camera)}/archive/audio?${q}`;
}

type URLs = { createObjectURL?: (b: Blob) => string; revokeObjectURL?: (u: string) => void };

export class ArchiveSound {
  private chunk = -1; // loaded chunk index, -1 for none
  private loading: number | null = null;
  private url: string | null = null;
  private abort: AbortController | null = null;
  private window: { camera: string; stream: string; origin: Date; seconds: number } | null = null;
  private readonly urls: Required<URLs>;
  private readonly onTime = () => void this.sync();
  private readonly onPause = () => this.audio.pause();
  private readonly onPlay = () => void this.sync(true);

  constructor(private readonly audio: HTMLMediaElement, private readonly video: HTMLMediaElement, private readonly source: ByteSource, urls: URLs = {}) {
    this.urls = {
      createObjectURL: urls.createObjectURL ?? ((b) => URL.createObjectURL(b)),
      revokeObjectURL: urls.revokeObjectURL ?? ((u) => URL.revokeObjectURL(u)),
    };
  }

  /**
   * Starts sound for the video's current window: `origin` is the moment the
   * video's time 0 shows, `seconds` the window's length. Rejects with the
   * hive's CamhiveError (no_audio, audio_unsupported, …) when there is none.
   */
  async start(camera: string, stream: string, origin: Date, seconds: number): Promise<void> {
    this.stop();
    this.window = { camera, stream, origin, seconds };
    this.video.addEventListener("timeupdate", this.onTime);
    this.video.addEventListener("seeked", this.onTime);
    this.video.addEventListener("play", this.onPlay);
    this.video.addEventListener("pause", this.onPause);
    await this.load(this.chunkAt(this.video.currentTime));
  }

  /** Stops the sound and forgets the window. */
  stop(): void {
    this.abort?.abort();
    this.abort = null;
    for (const [ev, fn] of [["timeupdate", this.onTime], ["seeked", this.onTime], ["play", this.onPlay], ["pause", this.onPause]] as const) {
      this.video.removeEventListener(ev, fn);
    }
    this.audio.pause();
    this.audio.removeAttribute("src");
    if (this.url) this.urls.revokeObjectURL(this.url);
    this.url = null;
    this.window = null;
    this.chunk = -1;
    this.loading = null;
  }

  private chunkAt(t: number): number {
    return Math.max(0, Math.floor(t / SOUND_CHUNK_S));
  }

  private async load(k: number): Promise<void> {
    const w = this.window;
    if (!w) return;
    this.loading = k;
    this.abort?.abort();
    const ctl = new AbortController();
    this.abort = ctl;
    const from = new Date(w.origin.getTime() + k * SOUND_CHUNK_S * 1000);
    const res = await this.source(audioPath(w.camera, w.stream, from, Math.min(SOUND_CHUNK_S, w.seconds - k * SOUND_CHUNK_S)), ctl.signal);
    if (!res.ok) {
      this.loading = null;
      throw new CamhiveError(res.status, await res.json().catch(() => undefined));
    }
    const blob = await res.blob();
    if (ctl.signal.aborted || this.window !== w) return;
    if (this.url) this.urls.revokeObjectURL(this.url);
    this.url = this.urls.createObjectURL(blob);
    this.audio.src = this.url;
    this.chunk = k;
    this.loading = null;
    this.sync(true);
  }

  /** Keeps the sound at the picture's time; loads the next minute when the picture gets there. */
  private sync(force = false): void {
    if (!this.window) return;
    const t = this.video.currentTime;
    const k = this.chunkAt(t);
    if (k !== this.chunk) {
      if (this.loading !== k) void this.load(k).catch(() => undefined);
      return;
    }
    const want = t - k * SOUND_CHUNK_S;
    if (force || Math.abs(this.audio.currentTime - want) > MAX_DRIFT_S) this.audio.currentTime = want;
    if (!this.video.paused && this.audio.paused) void this.audio.play()?.catch(() => undefined);
  }
}
