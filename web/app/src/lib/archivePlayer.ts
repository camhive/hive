// Plays archive footage: the hive answers GET /v0/cameras/{id}/archive/play
// with fragmented MP4 that needs a bearer token, which a <video src> cannot
// send. So the bytes are fetched (through the transport) and fed to the
// element: streamed into a MediaSource where the browser has one, otherwise
// collected into a Blob.
export type ByteSource = (path: string, signal: AbortSignal) => Promise<Response>;

export interface PlayerDeps {
  mediaSource?: (new () => MediaSource) | null;
  isTypeSupported?: (mime: string) => boolean;
  createObjectURL?: (b: Blob) => string;
  revokeObjectURL?: (u: string) => void;
}

export const DEFAULT_WINDOW_S = 600;

/** MIME for MSE from the camera's codec name; profile levels are hints, the init segment decides. */
export function mimeFor(codec: string | undefined): string {
  const c = (codec ?? "").toUpperCase();
  if (c.includes("265") || c.includes("HEVC")) return 'video/mp4; codecs="hvc1.1.6.L120.B0"';
  return 'video/mp4; codecs="avc1.640028"';
}

export function playPath(camera: string, stream: string, start: Date, durationS: number): string {
  const q = new URLSearchParams({ stream, start: start.toISOString(), duration: String(Math.max(1, Math.floor(durationS))) });
  return `/v0/cameras/${encodeURIComponent(camera)}/archive/play?${q}`;
}

export class ArchivePlayer {
  private abort: AbortController | null = null;
  private url: string | null = null;
  private readonly deps: Required<PlayerDeps>;

  constructor(private readonly video: HTMLVideoElement, private readonly source: ByteSource, deps: PlayerDeps = {}) {
    const g = globalThis as unknown as { MediaSource?: new () => MediaSource; URL: typeof URL };
    const ms = deps.mediaSource === undefined ? (g.MediaSource ?? null) : deps.mediaSource;
    this.deps = {
      mediaSource: ms,
      isTypeSupported: deps.isTypeSupported ?? ((m) => !!(ms as unknown as { isTypeSupported?: (m: string) => boolean } | null)?.isTypeSupported?.(m)),
      createObjectURL: deps.createObjectURL ?? ((b) => g.URL.createObjectURL(b)),
      revokeObjectURL: deps.revokeObjectURL ?? ((u) => g.URL.revokeObjectURL(u)),
    };
  }

  /** Starts playback from `start` for `durationS`; a running playback is cancelled. Resolves when the first bytes arrived. */
  async play(camera: string, stream: string, start: Date, durationS: number, codec?: string): Promise<"mse" | "blob"> {
    this.stop();
    const ctl = new AbortController();
    this.abort = ctl;
    const res = await this.source(playPath(camera, stream, start, durationS), ctl.signal);
    if (!res.ok) throw new Error(`playback ${res.status}`);
    const mime = mimeFor(codec);
    if (this.deps.mediaSource && this.deps.isTypeSupported(mime) && res.body) {
      await this.streamInto(res.body, mime, ctl.signal);
      return "mse";
    }
    const blob = await res.blob();
    if (ctl.signal.aborted) return "blob";
    this.url = this.deps.createObjectURL(blob);
    this.video.src = this.url;
    void this.video.play?.()?.catch(() => undefined);
    return "blob";
  }

  private streamInto(body: ReadableStream<Uint8Array>, mime: string, signal: AbortSignal): Promise<void> {
    const ms = new this.deps.mediaSource!();
    this.url = this.deps.createObjectURL(ms as unknown as Blob);
    this.video.src = this.url;
    return new Promise((resolve, reject) => {
      ms.addEventListener("sourceopen", () => {
        let sb: SourceBuffer;
        try {
          sb = ms.addSourceBuffer(mime);
        } catch (err) {
          reject(err);
          return;
        }
        const reader = body.getReader();
        let first = true;
        const pump = async () => {
          try {
            while (!signal.aborted) {
              const { done, value } = await reader.read();
              if (done) {
                if (ms.readyState === "open" && !sb.updating) ms.endOfStream();
                else sb.addEventListener("updateend", () => { if (ms.readyState === "open") ms.endOfStream(); }, { once: true });
                return;
              }
              if (sb.updating) await new Promise<void>((r) => sb.addEventListener("updateend", () => r(), { once: true }));
              if (signal.aborted) return;
              sb.appendBuffer(value as unknown as BufferSource);
              if (first) {
                first = false;
                void this.video.play?.()?.catch(() => undefined);
                resolve();
              }
            }
          } catch (err) {
            if (first) reject(err);
          }
        };
        void pump();
      }, { once: true });
    });
  }

  /** Cancels the download and releases the element. */
  stop(): void {
    this.abort?.abort();
    this.abort = null;
    if (this.url) {
      this.deps.revokeObjectURL(this.url);
      this.url = null;
    }
    try {
      this.video.removeAttribute("src");
      this.video.load?.();
    } catch {
      /* detached element */
    }
  }
}
