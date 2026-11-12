import { describe, expect, it, vi } from "vitest";
import { LiveSession, candidateFragment, type PeerConnection, type Signalling } from "../src/lib/live";

/** Enough of RTCPeerConnection to drive the session deterministically. */
class FakePC extends EventTarget implements PeerConnection {
  localDescription: RTCSessionDescription | null = null;
  iceGatheringState: RTCIceGatheringState = "gathering";
  connectionState: RTCPeerConnectionState = "new";
  transceivers: string[] = [];
  remote: RTCSessionDescriptionInit | null = null;
  closed = false;
  config: RTCConfiguration;
  constructor(config: RTCConfiguration) {
    super();
    this.config = config;
  }
  addTransceiver(kind: string): unknown {
    this.transceivers.push(kind);
    return {};
  }
  async createOffer(): Promise<RTCSessionDescriptionInit> {
    return { type: "offer", sdp: "v=0 offer" };
  }
  async setLocalDescription(d?: RTCLocalSessionDescriptionInit): Promise<void> {
    this.localDescription = { type: "offer", sdp: d?.sdp ?? "", toJSON: () => ({}) } as RTCSessionDescription;
    queueMicrotask(() => {
      this.iceGatheringState = "complete";
      this.dispatchEvent(new Event("icegatheringstatechange"));
    });
  }
  async setRemoteDescription(d: RTCSessionDescriptionInit): Promise<void> {
    this.remote = d;
  }
  close(): void {
    this.closed = true;
  }
  connect() {
    this.connectionState = "connected";
    this.dispatchEvent(new Event("connectionstatechange"));
  }
}

function fakeSignal(): Signalling & { calls: string[] } {
  const calls: string[] = [];
  return {
    calls,
    async offer(b) {
      calls.push(`offer ${b.camera} ${b.stream} ${b.sdp}`);
      return { sdp: "v=0 answer", session: "https://hive/whep/abc" };
    },
    async candidates(s, frag) {
      calls.push(`cand ${s} ${JSON.stringify(frag)}`);
    },
    async close(s) {
      calls.push(`close ${s}`);
    },
  };
}

describe("LiveSession", () => {
  it("offers recvonly video+audio, applies the answer, trickles late candidates, reports state and closes", async () => {
    const pcs: FakePC[] = [];
    const signal = fakeSignal();
    const states: string[] = [];
    const tracks: MediaStream[] = [];
    const s = await LiveSession.start({
      signal, camera: "cam_1", stream: "sub", iceServers: [{ urls: ["stun:stun.camhive.app:3478"] }],
      peer: (c) => { const pc = new FakePC(c); pcs.push(pc); return pc; },
      onState: (st) => states.push(st), onTrack: (ms) => tracks.push(ms),
    });
    const pc = pcs[0]!;
    expect(pc.config.iceServers).toEqual([{ urls: ["stun:stun.camhive.app:3478"] }]);
    expect(pc.transceivers).toEqual(["video", "audio"]);
    expect(signal.calls[0]).toBe("offer cam_1 sub v=0 offer");
    expect(pc.remote).toEqual({ type: "answer", sdp: "v=0 answer" });

    const ev = new Event("icecandidate") as Event & { candidate: unknown };
    ev.candidate = { candidate: "candidate:1 1 udp 2 192.168.1.5 5000 typ host", sdpMid: "0" };
    pc.dispatchEvent(ev);
    await Promise.resolve();
    expect(signal.calls[1]).toBe('cand https://hive/whep/abc "a=mid:0\\r\\na=candidate:1 1 udp 2 192.168.1.5 5000 typ host\\r\\n"');

    const trackEv = new Event("track") as Event & { streams: unknown[] };
    trackEv.streams = [{ id: "ms" }];
    pc.dispatchEvent(trackEv);
    expect(tracks).toHaveLength(1);

    pc.connect();
    expect(s.state).toBe("connected");
    await s.stop();
    expect(pc.closed).toBe(true);
    expect(signal.calls.at(-1)).toBe("close https://hive/whep/abc");
    expect(states).toEqual(["connected", "closed"]);
    // No second close for a stopped session.
    await s.stop();
    expect(signal.calls.filter((c) => c.startsWith("close"))).toHaveLength(1);
  });

  it("sends the offer after the gathering timeout when ICE never completes", async () => {
    const signal = fakeSignal();
    class SlowPC extends FakePC {
      override async setLocalDescription(d?: RTCLocalSessionDescriptionInit): Promise<void> {
        this.localDescription = { type: "offer", sdp: d?.sdp ?? "", toJSON: () => ({}) } as RTCSessionDescription;
      }
    }
    const t0 = Date.now();
    await LiveSession.start({ signal, camera: "cam", peer: (c) => new SlowPC(c), gatherTimeoutMs: 30 });
    expect(Date.now() - t0).toBeGreaterThanOrEqual(25);
    expect(signal.calls[0]).toMatch(/^offer cam main/);
  });

  it("fails cleanly when the hive refuses the offer", async () => {
    const pcs: FakePC[] = [];
    const signal = fakeSignal();
    signal.offer = async () => { throw new Error("hive_unreachable"); };
    const onState = vi.fn();
    await expect(LiveSession.start({ signal, camera: "cam", peer: (c) => { const pc = new FakePC(c); pcs.push(pc); return pc; }, onState })).rejects.toThrow("hive_unreachable");
    expect(pcs[0]!.closed).toBe(true);
    expect(onState).toHaveBeenCalledWith("failed");
  });

  it("formats trickle fragments per RFC 8840", () => {
    expect(candidateFragment({ candidate: "candidate:x", sdpMid: "1" })).toBe("a=mid:1\r\na=candidate:x\r\n");
    expect(candidateFragment({ candidate: "candidate:x", sdpMid: null })).toBe("a=mid:0\r\na=candidate:x\r\n");
  });
});
