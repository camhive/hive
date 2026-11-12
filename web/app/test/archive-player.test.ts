import { afterEach, describe, expect, it, vi } from "vitest";
import { ArchivePlayer } from "../src/lib/archivePlayer";

function fakeVideo() {
  const v = { src: "", play: vi.fn(async () => undefined), load: vi.fn(), removeAttribute: vi.fn(function (this: { src: string }) { this.src = ""; }) };
  return v as unknown as HTMLVideoElement & typeof v;
}
const chunks = [new Uint8Array([1, 2]), new Uint8Array([3])];
const body = () => new ReadableStream<Uint8Array>({ start(c) { for (const ch of chunks) c.enqueue(ch); c.close(); } });

/** MediaSource that opens on attach and records appended bytes. */
class FakeMS extends EventTarget {
  static last: FakeMS | null = null;
  /** Appends after this many succeed throw, as after the browser rejected the bytes. */
  static failAfter = Infinity;
  readyState = "closed";
  appended: number[] = [];
  mime = "";
  ended = false;
  constructor() { super(); FakeMS.last = this; }
  addSourceBuffer(mime: string) {
    const ms = this;
    ms.mime = mime;
    let calls = 0;
    const sb = Object.assign(new EventTarget(), {
      updating: false,
      appendBuffer(b: Uint8Array) {
        if (calls++ >= FakeMS.failAfter) throw new DOMException("source buffer removed", "InvalidStateError");
        ms.appended.push(...b); queueMicrotask(() => sb.dispatchEvent(new Event("updateend")));
      },
    });
    return sb as unknown as SourceBuffer;
  }
  endOfStream() { this.ended = true; }
  open() { this.readyState = "open"; this.dispatchEvent(new Event("sourceopen")); }
}

describe("ArchivePlayer", () => {
  it("streams fMP4 into a MediaSource when supported and starts playback on the first chunk", async () => {
    const video = fakeVideo();
    const source = vi.fn(async (path: string) => { expect(path).toContain("/v0/cameras/cam_1/archive/play?stream=main"); return new Response(body(), { status: 200 }); });
    const urls: string[] = [];
    const p = new ArchivePlayer(video, source, { mediaSource: FakeMS as unknown as new () => MediaSource, isTypeSupported: () => true, createObjectURL: () => { urls.push("blob:ms"); return "blob:ms"; }, revokeObjectURL: (u) => urls.push(`revoked ${u}`) });
    const done = p.play("cam_1", "main", new Date("2026-11-09T10:00:00Z"), 60, { main: "H264" });
    await Promise.resolve();
    FakeMS.last!.open();
    expect(await done).toBe("mse");
    expect(video.src).toBe("blob:ms");
    await vi.waitFor(() => expect(FakeMS.last!.ended).toBe(true));
    expect(FakeMS.last!.appended).toEqual([1, 2, 3]);
    expect(video.play).toHaveBeenCalled();
    p.stop();
    expect(urls).toEqual(["blob:ms", "revoked blob:ms"]);
    expect(video.load).toHaveBeenCalled();
  });

  it("falls back to a Blob URL without MediaSource and cancels the previous download on replay", async () => {
    const video = fakeVideo();
    const signals: AbortSignal[] = [];
    const source = vi.fn(async (_p: string, signal: AbortSignal) => { signals.push(signal); return new Response(body(), { status: 200 }); });
    const p = new ArchivePlayer(video, source, { mediaSource: null, createObjectURL: () => "blob:b", revokeObjectURL: () => undefined });
    expect(await p.play("cam_1", "sub", new Date(), 60)).toBe("blob");
    expect(video.src).toBe("blob:b");
    await p.play("cam_1", "sub", new Date(), 60);
    expect(signals[0]!.aborted).toBe(true);
    expect(signals[1]!.aborted).toBe(false);
  });

  afterEach(() => { FakeMS.failAfter = Infinity; });

  const mse = (extra: object = {}) => ({ mediaSource: FakeMS as unknown as new () => MediaSource, isTypeSupported: () => true, createObjectURL: () => "blob:ms", revokeObjectURL: () => undefined, ...extra });

  // Through the relay a reduced plan gets sub while the viewer picked main; the
  // bytes are sub's, so the decoder must be set up for sub's codec.
  it("sets the decoder up for the stream the hive served, not the one asked for", async () => {
    const res = () => new Response(body(), { status: 200, headers: { "X-Camhive-Stream": "sub" } });
    const p = new ArchivePlayer(fakeVideo(), async () => res(), mse());
    const done = p.play("cam_1", "main", new Date(), 60, { main: "H265", sub: "H264" });
    await Promise.resolve(); await Promise.resolve();
    FakeMS.last!.open();
    await done;
    expect(FakeMS.last!.mime).toContain("avc1");
  });

  it("uses the requested stream's codec when the hive does not name one", async () => {
    const p = new ArchivePlayer(fakeVideo(), async () => new Response(body(), { status: 200 }), mse());
    const done = p.play("cam_1", "main", new Date(), 60, { main: "H265", sub: "H264" });
    await Promise.resolve(); await Promise.resolve();
    FakeMS.last!.open();
    await done;
    expect(FakeMS.last!.mime).toContain("hvc1");
  });

  it("reports a failure after playback started instead of sitting still", async () => {
    FakeMS.failAfter = 1;
    const onError = vi.fn();
    const p = new ArchivePlayer(fakeVideo(), async () => new Response(body(), { status: 200 }), mse({ onError }));
    const done = p.play("cam_1", "main", new Date(), 60);
    await Promise.resolve(); await Promise.resolve();
    FakeMS.last!.open();
    await done;
    await vi.waitFor(() => expect(onError).toHaveBeenCalledTimes(1));
  });

  it("rejects an empty answer", async () => {
    const empty = new ReadableStream<Uint8Array>({ start(c) { c.close(); } });
    const p = new ArchivePlayer(fakeVideo(), async () => new Response(empty, { status: 200 }), mse());
    const done = p.play("cam_1", "main", new Date(), 60);
    await Promise.resolve(); await Promise.resolve();
    FakeMS.last!.open();
    await expect(done).rejects.toThrow("playback empty");
  });

  it("rejects on an error status", async () => {
    const p = new ArchivePlayer(fakeVideo(), async () => new Response("", { status: 502 }), { mediaSource: null });
    await expect(p.play("cam_1", "main", new Date(), 60)).rejects.toThrow("playback 502");
  });
});
