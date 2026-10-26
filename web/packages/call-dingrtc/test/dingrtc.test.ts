import type { CallMediaEngine, MediaJoin } from "@talkwire/call";
import {
  DingRtcMediaEngine,
  type DingRtcClient,
  type DingRtcJoinParam,
  type DingRtcLocalTrack,
  type DingRtcMediaType,
  type DingRtcRemoteTrack,
  type DingRtcSdk,
  type DingRtcUser,
} from "@talkwire/call-dingrtc";
import { describe, expect, it } from "vitest";

type Listener = (user: DingRtcUser, type: DingRtcMediaType, aux?: boolean) => void;

/** A fake DingRTC: records what the engine does, lets the test play the other side. */
function fakeSdk() {
  const log: string[] = [];
  const el = (name: string) => ({ id: name }) as unknown as HTMLElement;
  const name = (e?: string | HTMLElement) => (e === undefined ? "speaker" : typeof e === "string" ? e : (e as unknown as { id: string }).id);
  let n = 0;
  const local = (kind: string): DingRtcLocalTrack => {
    const id = `${kind}${++n}`;
    return {
      play: (e) => log.push(`${id} play ${name(e)}`),
      setMuted: async (m) => void log.push(`${id} muted ${m}`),
      close: () => log.push(`${id} close`),
    };
  };
  const listeners = new Map<string, Set<Listener>>();
  let joinGate: Promise<void> = Promise.resolve();
  let micGate: Promise<void> = Promise.resolve();
  const client: DingRtcClient = {
    async join(p: DingRtcJoinParam) {
      log.push(`join ${p.appId} ${p.channel} ${p.uid} ${p.userName} ${p.token}`);
      await joinGate;
      return { remoteUsers: existing };
    },
    leave: () => log.push("leave"),
    publish: async (t) => void log.push(`publish ${(Array.isArray(t) ? t : [t]).length}`),
    unpublish: async () => void log.push("unpublish"),
    async subscribe(uid, type): Promise<DingRtcRemoteTrack> {
      log.push(`subscribe ${uid} ${type}`);
      return { play: (e) => log.push(`remote ${uid} ${type} play ${name(e)}`) };
    },
    on: (e, fn) => listeners.set(e, (listeners.get(e) ?? new Set()).add(fn)),
    off: (e, fn) => listeners.get(e)?.delete(fn),
  };
  let existing: DingRtcUser[] = [];
  const sdk: DingRtcSdk = {
    createClient: () => client,
    async createMicrophoneAudioTrack(c) {
      log.push(`mic ANS=${c?.ANS}`);
      await micGate;
      return local("mic");
    },
    async createCameraVideoTrack(c) {
      log.push(`camera ${c.dimension} ${c.frameRate}`);
      return local("cam");
    },
  };
  const peer = (userId: string, hasVideo = false): DingRtcUser => ({ userId, hasVideo, hasAudio: true });
  const emit = (event: string, user: DingRtcUser, type: DingRtcMediaType) => {
    for (const fn of listeners.get(event) ?? []) fn(user, type);
  };
  return {
    sdk,
    log,
    el,
    peer,
    emit,
    listening: () => [...listeners.values()].reduce((s, l) => s + l.size, 0),
    existing: (users: DingRtcUser[]) => (existing = users),
    holdJoin: () => {
      let release!: () => void;
      joinGate = new Promise((r) => (release = r));
      return release;
    },
    holdMic: () => {
      let release!: () => void;
      micGate = new Promise((r) => (release = r));
      return release;
    },
  };
}

const settle = () => new Promise((r) => setTimeout(r, 0));

const join = (callType: "voice" | "video", extra: Partial<MediaJoin["credentials"]> = {}): MediaJoin => ({
  uid: "uid_K9gGyX8OAK8",
  callType,
  credentials: { channelId: "ch-1", rtcToken: "tok-a", appId: "app1", denoiseEnabled: true, ...extra },
});

describe("DingRtcMediaEngine", () => {
  it("is a CallMediaEngine", () => {
    const engine: CallMediaEngine = new DingRtcMediaEngine(fakeSdk().sdk);
    expect(engine).toBeTruthy();
  });

  it("joins a voice call as the IM uid, publishes the microphone and plays the mixed audio once", async () => {
    const f = fakeSdk();
    const engine = new DingRtcMediaEngine(f.sdk);
    await engine.join(join("voice"));
    expect(f.log).toEqual(["join app1 ch-1 uid_K9gGyX8OAK8 uid_K9gGyX8OAK8 tok-a", "mic ANS=true", "publish 1"]);
    f.emit("user-published", f.peer("uid_peer"), "audio");
    f.emit("user-published", f.peer("uid_peer"), "audio");
    await settle();
    expect(f.log.slice(3)).toEqual(["subscribe mcu audio", "remote mcu audio play speaker"]);
  });

  it("video: the camera at the host's quality, local and remote video wherever they are attached", async () => {
    const f = fakeSdk();
    const engine = new DingRtcMediaEngine(f.sdk);
    engine.attachLocalVideo(f.el("local"));
    await engine.join(join("video", { videoQuality: "hd" }));
    expect(f.log).toContain("camera VD_1280x720 15");
    expect(f.log).toContain("cam2 play local");
    f.emit("user-published", f.peer("uid_peer"), "video");
    await settle();
    expect(f.log.at(-1)).toBe("subscribe uid_peer video");
    engine.attachRemoteVideo(f.el("remote")); // the screen mounted after the video arrived
    expect(f.log.at(-1)).toBe("remote uid_peer video play remote");
    f.emit("user-unpublished", f.peer("uid_peer"), "video");
    engine.attachRemoteVideo(f.el("remote2"));
    expect(f.log.at(-1)).toBe("remote uid_peer video play remote");
  });

  it("upgrades to video and back; mute holds across the microphone's creation", async () => {
    const f = fakeSdk();
    const engine = new DingRtcMediaEngine(f.sdk);
    engine.setMuted(true);
    await engine.join(join("voice", { denoiseEnabled: false }));
    expect(f.log).toContain("mic ANS=false");
    expect(f.log).toContain("mic1 muted true");
    await engine.setVideo(true);
    expect(f.log.slice(-2)).toEqual(["camera VD_640x480 15", "publish 1"]);
    await engine.setVideo(true); // twice is once
    expect(f.log.filter((l) => l.startsWith("camera"))).toHaveLength(1);
    await engine.setVideo(false);
    expect(f.log.slice(-2)).toEqual(["unpublish", "cam2 close"]);
    engine.setMuted(false);
    await settle();
    expect(f.log.at(-1)).toBe("mic1 muted false");
  });

  it("subscribes to peers already in the channel", async () => {
    const f = fakeSdk();
    f.existing([f.peer("uid_peer", true)]);
    const engine = new DingRtcMediaEngine(f.sdk);
    await engine.join(join("video"));
    await settle();
    expect(f.log).toContain("subscribe mcu audio");
    expect(f.log).toContain("subscribe uid_peer video");
  });

  it("hung up while connecting: nothing stays on", async () => {
    const f = fakeSdk();
    const engine = new DingRtcMediaEngine(f.sdk);
    const releaseMic = f.holdMic();
    const joining = engine.join(join("video"));
    await settle();
    expect(f.log.at(-1)).toBe("mic ANS=true");
    await engine.leave();
    releaseMic();
    await joining;
    expect(f.log.slice(-3)).toEqual(["mic ANS=true", "leave", "mic1 close"]);
    expect(f.log.some((l) => l.startsWith("publish") || l.startsWith("camera"))).toBe(false);
    expect(f.listening()).toBe(0);

    // A peer announced after the hang-up is not subscribed.
    f.emit("user-published", f.peer("uid_late"), "video");
    await settle();
    expect(f.log.some((l) => l.includes("uid_late"))).toBe(false);
    await engine.leave(); // leaving twice is fine
  });

  it("leave during join itself, a missing app id, and a host's own user ids", async () => {
    const f = fakeSdk();
    const engine = new DingRtcMediaEngine(f.sdk, { userId: (p) => "host-user-of-" + p.uid.slice(4), userName: () => "Alice" });
    const release = f.holdJoin();
    const joining = engine.join(join("voice"));
    await settle();
    await engine.leave();
    release();
    await joining;
    expect(f.log).toEqual(["join app1 ch-1 host-user-of-K9gGyX8OAK8 Alice tok-a", "leave"]);
    await expect(new DingRtcMediaEngine(f.sdk).join(join("voice", { appId: undefined }))).rejects.toThrow(/app id/);
  });
});
