// Live video: WHEP over WebRTC. Signalling goes straight to the hive at home
// and through the cloud otherwise (ADR-0010 path 2, ADR-0013). The browser gathers ICE
// candidates, posts the offer, gets the hive's answer and trickles late
// candidates; media flows peer to peer or through TURN with the credentials
// carried in the access grant.
import { unwrap, unwrapHive, type HiveClient, type ICEServer } from "@camhive/api";
import { api } from "./api";

export interface Signalling {
  offer(body: { camera: string; sdp: string; stream: string }): Promise<{ sdp: string; session: string; ice_servers?: ICEServer[] }>;
  candidates(session: string, fragment: string): Promise<void>;
  close(session: string): Promise<void>;
}

/** Signalling through the cloud API for one hive. */
export function apiSignalling(hiveID: string): Signalling {
  const path = { params: { path: { id: hiveID } } };
  return {
    async offer(body) {
      const r = unwrap(await api.POST("/v1/hives/{id}/signal/offer", { ...path, body }));
      return { sdp: r.sdp ?? "", session: r.session ?? "", ice_servers: r.ice_servers };
    },
    async candidates(session, fragment) {
      unwrap(await api.POST("/v1/hives/{id}/signal/candidates", { ...path, body: { session, candidates: fragment } }));
    },
    async close(session) {
      unwrap(await api.POST("/v1/hives/{id}/signal/close", { ...path, body: { session } }));
    },
  };
}

/** Signalling straight to the hive at home: LAN path or local mode (ADR-0013 / #56). The hive returns no ICE servers; host candidates connect. */
export function hiveSignalling(client: HiveClient): Signalling {
  return {
    async offer(body) {
      const r = unwrapHive(await client.POST("/v0/cameras/{id}/live/offer", { params: { path: { id: body.camera } }, body: { sdp: body.sdp, stream: body.stream as "main" | "sub" } }));
      return { sdp: r.sdp, session: r.session, ice_servers: [] };
    },
    async candidates(session, fragment) {
      unwrapHive(await client.POST("/v0/live/candidates", { body: { session, candidates: fragment } }));
    },
    async close(session) {
      unwrapHive(await client.POST("/v0/live/close", { body: { session } }));
    },
  };
}

/** RFC 8840 trickle fragment for one candidate: the m-section id and the candidate line. */
export function candidateFragment(c: Pick<RTCIceCandidate, "candidate" | "sdpMid">): string {
  return `a=mid:${c.sdpMid ?? "0"}\r\na=${c.candidate}\r\n`;
}

/** What the session needs from RTCPeerConnection; tests pass a fake with the same shape. */
export interface PeerConnection {
  readonly localDescription: RTCSessionDescription | null;
  readonly iceGatheringState: RTCIceGatheringState;
  readonly connectionState: RTCPeerConnectionState;
  addTransceiver(kind: string, init?: RTCRtpTransceiverInit): unknown;
  createOffer(): Promise<RTCSessionDescriptionInit>;
  setLocalDescription(d?: RTCLocalSessionDescriptionInit): Promise<void>;
  setRemoteDescription(d: RTCSessionDescriptionInit): Promise<void>;
  close(): void;
  addEventListener(type: string, listener: (e: Event) => void): void;
  removeEventListener(type: string, listener: (e: Event) => void): void;
}
export type PeerFactory = (config: RTCConfiguration) => PeerConnection;

export type LiveState = "connecting" | "connected" | "failed" | "closed";

export interface LiveOptions {
  signal: Signalling;
  camera: string;
  stream?: "main" | "sub";
  iceServers?: ICEServer[];
  /** Receives the remote stream (video.srcObject). */
  onTrack?: (stream: MediaStream) => void;
  onState?: (s: LiveState) => void;
  peer?: PeerFactory;
  /** How long to wait for ICE gathering before sending the offer anyway (late candidates trickle). */
  gatherTimeoutMs?: number;
}

export class LiveSession {
  private pc: PeerConnection | null = null;
  private session = "";
  private readonly opts: LiveOptions;
  state: LiveState = "connecting";

  private constructor(opts: LiveOptions) {
    this.opts = opts;
  }

  /** Opens a session: offer → cloud → hive → answer. Rejects when the hive refuses or is unreachable. */
  static async start(opts: LiveOptions): Promise<LiveSession> {
    const s = new LiveSession(opts);
    await s.connect();
    return s;
  }

  private setState(st: LiveState) {
    if (this.state === st || this.state === "closed") return;
    this.state = st;
    this.opts.onState?.(st);
  }

  private async connect(): Promise<void> {
    const make: PeerFactory = this.opts.peer ?? ((c) => new RTCPeerConnection(c));
    const pc = make({ iceServers: this.opts.iceServers as RTCIceServer[] | undefined });
    this.pc = pc;
    pc.addEventListener("track", (e) => {
      const ev = e as RTCTrackEvent;
      const stream = ev.streams[0];
      if (stream) this.opts.onTrack?.(stream);
    });
    pc.addEventListener("connectionstatechange", () => {
      switch (pc.connectionState) {
        case "connected": this.setState("connected"); break;
        case "failed": this.setState("failed"); break;
        case "closed": this.setState("closed"); break;
        default: break;
      }
    });
    pc.addTransceiver("video", { direction: "recvonly" });
    pc.addTransceiver("audio", { direction: "recvonly" });
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);
    await waitForGathering(pc, this.opts.gatherTimeoutMs ?? 1000);
    const sdp = pc.localDescription?.sdp ?? offer.sdp ?? "";
    let answer: Awaited<ReturnType<Signalling["offer"]>>;
    try {
      answer = await this.opts.signal.offer({ camera: this.opts.camera, sdp, stream: this.opts.stream ?? "main" });
    } catch (err) {
      this.setState("failed");
      pc.close();
      throw err;
    }
    this.session = answer.session;
    // Candidates found after the offer went out are trickled to the hive.
    pc.addEventListener("icecandidate", (e) => {
      const c = (e as RTCPeerConnectionIceEvent).candidate;
      if (c && c.candidate && this.session) void this.opts.signal.candidates(this.session, candidateFragment(c)).catch(() => undefined);
    });
    await pc.setRemoteDescription({ type: "answer", sdp: answer.sdp });
  }

  /** Closes the peer connection and tells the hive to drop the session. */
  async stop(): Promise<void> {
    const session = this.session;
    this.session = "";
    this.pc?.close();
    this.pc = null;
    this.setState("closed");
    if (session) await this.opts.signal.close(session).catch(() => undefined);
  }
}

function waitForGathering(pc: PeerConnection, timeoutMs: number): Promise<void> {
  if (pc.iceGatheringState === "complete") return Promise.resolve();
  return new Promise((resolve) => {
    const done = () => {
      clearTimeout(timer);
      pc.removeEventListener("icegatheringstatechange", check);
      resolve();
    };
    const check = () => {
      if (pc.iceGatheringState === "complete") done();
    };
    const timer = setTimeout(done, timeoutMs);
    pc.addEventListener("icegatheringstatechange", check);
  });
}
