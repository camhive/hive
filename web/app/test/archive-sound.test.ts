import { describe, expect, it, vi } from "vitest";
import { CamhiveError } from "@camhive/api";
import { ArchiveSound, audioPath, SOUND_CHUNK_S } from "../src/lib/archiveSound";

/** A media element: current time, paused flag, events. */
function media(paused = false) {
  const m = Object.assign(new EventTarget(), { currentTime: 0, paused, src: "", play: vi.fn(async () => undefined), pause: vi.fn(), removeAttribute: vi.fn() });
  return m as unknown as HTMLMediaElement & typeof m;
}
const origin = new Date("2026-11-09T10:00:00Z");
const wav = () => new Response(new Blob(["RIFF"]), { status: 200 });

describe("ArchiveSound (ADR-0015)", () => {
  it("plays the minute the video is in, follows it into the next minute, and corrects drift", async () => {
    const video = media(), audio = media(true);
    const asked: string[] = [];
    const s = new ArchiveSound(audio, video, async (path) => { asked.push(path); return wav(); }, { createObjectURL: () => `blob:${asked.length}`, revokeObjectURL: () => undefined });
    await s.start("cam_1", "main", origin, 150);
    expect(asked).toEqual([audioPath("cam_1", "main", origin, SOUND_CHUNK_S)]);
    expect(audio.src).toBe("blob:1");
    expect(audio.play).toHaveBeenCalled();

    // Drift over 0.3 s is corrected; small drift is left alone.
    video.currentTime = 10; audio.currentTime = 10.2;
    video.dispatchEvent(new Event("timeupdate"));
    expect(audio.currentTime).toBe(10.2);
    audio.currentTime = 11;
    video.dispatchEvent(new Event("timeupdate"));
    expect(audio.currentTime).toBe(10);

    // Into the third minute of a 150 s window: the last 30 s are asked for.
    video.currentTime = 125;
    video.dispatchEvent(new Event("timeupdate"));
    await vi.waitFor(() => expect(asked).toHaveLength(2));
    expect(asked[1]).toBe(audioPath("cam_1", "main", new Date(origin.getTime() + 120_000), 30));
    await vi.waitFor(() => expect(audio.currentTime).toBe(5));

    // Pausing the video pauses the sound; stop releases everything.
    video.dispatchEvent(new Event("pause"));
    expect(audio.pause).toHaveBeenCalled();
    s.stop();
    video.currentTime = 10;
    video.dispatchEvent(new Event("timeupdate"));
    expect(asked).toHaveLength(2);
  });

  it("rejects with the hive's code when there is no sound", async () => {
    const s = new ArchiveSound(media(), media(), async () => new Response(JSON.stringify({ code: "no_audio", message: "x" }), { status: 404 }), { createObjectURL: () => "blob:x", revokeObjectURL: () => undefined });
    const err = await s.start("cam_1", "main", origin, 60).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(CamhiveError);
    expect((err as CamhiveError).code).toBe("no_audio");
  });
  it("plays at the video's speed", async () => {
    const video = Object.assign(media(), { playbackRate: 1 }), audio = Object.assign(media(true), { playbackRate: 1 });
    const s = new ArchiveSound(audio, video, async () => wav(), { createObjectURL: () => "blob:1", revokeObjectURL: () => undefined });
    await s.start("cam_1", "main", origin, 60);
    video.playbackRate = 4;
    video.dispatchEvent(new Event("ratechange"));
    expect(audio.playbackRate).toBe(4);
  });
});
