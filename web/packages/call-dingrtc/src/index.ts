/**
 * @imkit/call-dingrtc: a CallMediaEngine (see @imkit/call) over Alibaba
 * Cloud's DingRTC Web SDK (ARTC 3.0, npm `dingrtc`; written against 3.9.37).
 * The app installs the SDK and hands it in, so nothing here loads it:
 *
 *   import DingRTC from "dingrtc";
 *   import { DingRtcMediaEngine } from "@imkit/call-dingrtc";
 *   <CallProvider media={new DingRtcMediaEngine(DingRTC)}>…</CallProvider>
 *
 * The host answers calls/answer with a DingRTC channel, the app id and each
 * side's token. The token names the user it is for; by default that is the
 * user's IM uid ("uid_…", which DingRTC accepts as a user id). A host that
 * signs tokens for its own user ids passes `userId`.
 */

import type { CallMediaEngine, MediaJoin } from "@imkit/call";

// ------------------------------------------------------------------ the SDK, as far as this uses it

export type DingRtcMediaType = "audio" | "video";

export interface DingRtcJoinParam {
  appId: string;
  token: string;
  uid: string;
  channel: string;
  userName: string;
}

export interface DingRtcLocalTrack {
  play(element?: string | HTMLElement): void;
  setMuted(muted: boolean): Promise<void>;
  close(): void;
}

export interface DingRtcRemoteTrack {
  play(element?: string | HTMLElement): void;
}

export interface DingRtcUser {
  readonly userId: string;
  readonly hasVideo: boolean;
  readonly hasAudio: boolean;
}

export interface DingRtcClient {
  join(param: DingRtcJoinParam): Promise<{ remoteUsers: DingRtcUser[] }>;
  leave(): void;
  publish(tracks: DingRtcLocalTrack | DingRtcLocalTrack[]): Promise<void>;
  unpublish(tracks?: DingRtcLocalTrack | DingRtcLocalTrack[]): Promise<void>;
  subscribe(uid: string, mediaType: DingRtcMediaType, auxiliary?: boolean): Promise<DingRtcRemoteTrack>;
  on(event: "user-published" | "user-unpublished", listener: (user: DingRtcUser, mediaType: DingRtcMediaType, auxiliary?: boolean) => void): unknown;
  off(event: "user-published" | "user-unpublished", listener: (user: DingRtcUser, mediaType: DingRtcMediaType, auxiliary?: boolean) => void): unknown;
}

export type DingRtcDimension = "VD_640x480" | "VD_1280x720" | (string & {});

export interface DingRtcSdk {
  createClient(): DingRtcClient;
  createMicrophoneAudioTrack(config?: { ANS?: boolean; AEC?: boolean; AGC?: boolean }): Promise<DingRtcLocalTrack>;
  createCameraVideoTrack(config: { dimension?: DingRtcDimension; frameRate?: number; facingMode?: "user" | "environment" }): Promise<DingRtcLocalTrack>;
}

// ------------------------------------------------------------------ the engine

export interface DingRtcEngineOptions {
  /** The DingRTC user id the host signed the token for; default the IM uid. */
  userId?(join: MediaJoin): string;
  /** The name others see in the channel; default the user id. */
  userName?(join: MediaJoin): string;
  /** Camera size for a quality the host names ("sd", "hd"); default sd → 640×480, hd → 1280×720. */
  dimension?(videoQuality: string | undefined): DingRtcDimension;
  frameRate?: number;
  /** What went wrong after join returned (a remote track that would not play); default console.warn. */
  onError?(err: unknown, what: string): void;
}

/** The mixed audio of everyone else in the channel (DingRTC subscribes audio once, by this name). */
const MIXED_AUDIO = "mcu";

/**
 * One call at a time. `leave` may come at any moment, also while `join` is
 * still on its way: whatever join creates after that is closed again, so a
 * call that was hung up while connecting never leaves the camera or the
 * microphone on.
 */
export class DingRtcMediaEngine implements CallMediaEngine {
  private client: DingRtcClient | null = null;
  private mic: DingRtcLocalTrack | null = null;
  private camera: DingRtcLocalTrack | null = null;
  private remoteVideo: DingRtcRemoteTrack | null = null;
  private remoteVideoOf: string | null = null;
  private audioSubscribed = false;
  private localEl: HTMLElement | null = null;
  private remoteEl: HTMLElement | null = null;
  private muted = false;
  private quality: string | undefined;
  /** Bumped by leave: work started before it must not touch the next call. */
  private generation = 0;
  private readonly onPublished = (user: DingRtcUser, type: DingRtcMediaType, aux?: boolean) => void this.subscribe(user, type, aux, this.generation);
  private readonly onUnpublished = (user: DingRtcUser, type: DingRtcMediaType) => {
    if (type === "video" && user.userId === this.remoteVideoOf) {
      this.remoteVideo = null;
      this.remoteVideoOf = null;
    }
  };

  constructor(
    private readonly sdk: DingRtcSdk,
    private readonly options: DingRtcEngineOptions = {},
  ) {}

  async join(p: MediaJoin): Promise<void> {
    const appId = p.credentials.appId;
    if (!appId) throw new Error("DingRTC needs the app id: the host's calls/answer must return appId");
    await this.leave(); // never two calls in one engine
    const gen = this.generation;
    const userId = this.options.userId?.(p) ?? p.uid;
    const client = this.sdk.createClient();
    this.client = client;
    this.quality = p.credentials.videoQuality;
    // Listen before joining: a peer already publishing is announced during join.
    client.on("user-published", this.onPublished);
    client.on("user-unpublished", this.onUnpublished);
    const joined = await client.join({
      appId,
      token: p.credentials.rtcToken,
      uid: userId,
      channel: p.credentials.channelId,
      userName: this.options.userName?.(p) ?? userId,
    });
    if (gen !== this.generation) return;
    for (const user of joined.remoteUsers ?? []) {
      if (user.hasAudio) void this.subscribe(user, "audio", false, gen);
      if (user.hasVideo) void this.subscribe(user, "video", false, gen);
    }
    const mic = await this.sdk.createMicrophoneAudioTrack({ ANS: p.credentials.denoiseEnabled });
    if (gen !== this.generation) return mic.close();
    this.mic = mic;
    if (this.muted) await mic.setMuted(true);
    await client.publish(mic);
    if (p.callType === "video") await this.startCamera(gen);
  }

  async setVideo(on: boolean, videoQuality?: string): Promise<void> {
    if (videoQuality) this.quality = videoQuality;
    if (on) return this.startCamera(this.generation);
    const camera = this.camera;
    this.camera = null;
    if (camera) {
      await this.client?.unpublish(camera).catch(() => {});
      camera.close();
    }
  }

  setMuted(muted: boolean): void {
    this.muted = muted;
    this.mic?.setMuted(muted).catch((err: unknown) => this.report(err, "mute"));
  }

  async leave(): Promise<void> {
    this.generation++;
    const { client, mic, camera } = this;
    this.client = this.mic = this.camera = this.remoteVideo = null;
    this.remoteVideoOf = null;
    this.audioSubscribed = false;
    for (const track of [mic, camera]) track?.close();
    if (client) {
      client.off("user-published", this.onPublished);
      client.off("user-unpublished", this.onUnpublished);
      try {
        client.leave();
      } catch (err) {
        this.report(err, "leave");
      }
    }
  }

  attachLocalVideo(el: HTMLElement | null): void {
    this.localEl = el;
    if (el && this.camera) this.camera.play(el);
  }

  attachRemoteVideo(el: HTMLElement | null): void {
    this.remoteEl = el;
    if (el && this.remoteVideo) this.remoteVideo.play(el);
  }

  private async startCamera(gen: number): Promise<void> {
    if (this.camera || !this.client || gen !== this.generation) return;
    const dimension = this.options.dimension?.(this.quality) ?? (this.quality === "hd" ? "VD_1280x720" : "VD_640x480");
    const camera = await this.sdk.createCameraVideoTrack({ dimension, frameRate: this.options.frameRate ?? 15 });
    if (gen !== this.generation || this.camera) return camera.close();
    this.camera = camera;
    if (this.localEl) camera.play(this.localEl);
    await this.client.publish(camera);
  }

  private async subscribe(user: DingRtcUser, type: DingRtcMediaType, auxiliary: boolean | undefined, gen: number): Promise<void> {
    const client = this.client;
    if (!client || gen !== this.generation || auxiliary) return; // screen sharing is not part of a call
    try {
      if (type === "audio") {
        if (this.audioSubscribed) return;
        this.audioSubscribed = true;
        const track = await client.subscribe(MIXED_AUDIO, "audio");
        if (gen === this.generation) track.play();
        return;
      }
      const track = await client.subscribe(user.userId, "video");
      if (gen !== this.generation) return;
      this.remoteVideo = track;
      this.remoteVideoOf = user.userId;
      if (this.remoteEl) track.play(this.remoteEl);
    } catch (err) {
      if (type === "audio") this.audioSubscribed = false;
      this.report(err, `subscribe ${type}`);
    }
  }

  private report(err: unknown, what: string): void {
    if (this.options.onError) this.options.onError(err, what);
    else console.warn(`DingRTC: ${what} failed`, err);
  }
}
