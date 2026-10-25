// The call module's host side over ARTC 3.0: CallHandlers for
// imkitHostPlugin that open one RTC channel per call and give each side its
// own token. Who may call whom and what a call costs stay with the host
// (canCall, onSettle); wrap the handlers in recordingCalls for the admin
// pages.
//
//   const calls = artcCalls({ appId, appKey, canCall: friendsOnly(contacts), onSettle: bill });
//   await app.register(imkitHostPlugin, { ..., calls: recordingCalls(calls, callRecords) });

import crypto from "node:crypto";
import { imUid, type CallHandlers, type ContactStore } from "@imkit/host-node";
import { MAX_TOKEN_SECONDS, buildAppToken } from "./app-token.js";

export type CallType = "voice" | "video";

export interface ArtcCall {
  callSessionId: string;
  callerUserId: string;
  calleeUserId: string;
  callType: CallType;
  channelId: string;
  /** Unix milliseconds; absent until answered. */
  answeredAt?: number;
}

export type CallPolicy = (callerUserId: string, calleeUserId: string) => Promise<{ ok: true } | { ok: false; reason: string }>;

export interface ArtcCallsOptions {
  appId: string;
  /** The app's secret key: it signs tokens and never leaves the host. */
  appKey: string;
  /** Who may call whom; default anyone. */
  canCall?: CallPolicy;
  /**
   * The RTC user id a token is for. Default the user's IM uid, which is what
   * @imkit/call-dingrtc joins as unless it is told otherwise; the two must agree.
   */
  rtcUserId?(userId: string): string;
  videoQuality?(): "sd" | "hd";
  denoiseEnabled?: boolean;
  /** How long a token is good for; at most and by default 24 hours. */
  tokenSeconds?: number;
  /** Once per call when it ends: bill it here. */
  onSettle?(call: ArtcCall, reason: string): Promise<void> | void;
  now?(): number;
}

/** A CallPolicy: only accepted contacts may call each other. */
export function friendsOnly(contacts: ContactStore): CallPolicy {
  return async (a, b) => ((await contacts.findPair(a, b))?.status === "accepted" ? { ok: true } : { ok: false, reason: "not_friends" });
}

export function artcCalls(o: ArtcCallsOptions): CallHandlers & { readonly calls: ReadonlyMap<string, ArtcCall> } {
  const calls = new Map<string, ArtcCall>();
  const now = o.now ?? Date.now;
  const rtcUserId = o.rtcUserId ?? imUid;
  const seconds = Math.min(o.tokenSeconds ?? MAX_TOKEN_SECONDS, MAX_TOKEN_SECONDS);
  const token = (call: ArtcCall, userId: string) =>
    buildAppToken({ appId: o.appId, appKey: o.appKey, channelId: call.channelId, userId: rtcUserId(userId), expiresAt: Math.floor(now() / 1000) + seconds });
  const quality = () => o.videoQuality?.() ?? "sd";

  return {
    calls,
    async create(req) {
      const allowed = o.canCall ? await o.canCall(req.callerAppUserId, req.calleeAppUserId) : { ok: true as const };
      if (!allowed.ok) return { ok: false, reason: allowed.reason };
      calls.set(req.callSessionId, {
        callSessionId: req.callSessionId,
        callerUserId: req.callerAppUserId,
        calleeUserId: req.calleeAppUserId,
        callType: req.callType,
        // One channel per call; DingRTC allows letters, digits, "_" and "-", up to 64.
        channelId: "imkit-" + crypto.randomBytes(12).toString("hex"),
      });
      return { ok: true };
    },
    async answer(req) {
      const call = calls.get(req.callSessionId);
      if (!call) return { ok: false, reason: "not_found" };
      call.callType = req.effectiveType;
      call.answeredAt = now();
      return {
        ok: true,
        channelId: call.channelId,
        appId: o.appId,
        // Each side's own token: the IM server hands each only theirs.
        callerToken: token(call, call.callerUserId),
        calleeToken: token(call, call.calleeUserId),
        videoQuality: quality(),
        denoiseEnabled: o.denoiseEnabled ?? true,
      };
    },
    async upgrade(req) {
      const call = calls.get(req.callSessionId);
      if (!call) return { ok: false, reason: "not_found" };
      call.callType = "video";
      return { ok: true, videoQuality: quality() };
    },
    async settle(req) {
      const call = calls.get(req.callSessionId);
      if (!call) return;
      calls.delete(req.callSessionId);
      await o.onSettle?.(call, req.reason);
    },
  };
}
