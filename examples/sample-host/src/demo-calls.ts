// The call module's host side for the samples: contacts may call each other,
// and answering hands out made-up RTC credentials. Signaling works end to end
// (ringing, answering, switching to video, hanging up); no audio or video
// flows, because no RTC service is behind the credentials. A real host gets
// them from its RTC provider (see examples/artc for an adapter) and bills the
// call in `settle`.

import type { CallHandlers, ContactStore } from "@imkit/host-node";

interface DemoCall {
  callerAppUserId: string;
  calleeAppUserId: string;
  callType: "voice" | "video";
}

export function demoCalls(contacts: ContactStore, log: (msg: string) => void = () => {}): CallHandlers & { readonly calls: ReadonlyMap<string, DemoCall> } {
  const calls = new Map<string, DemoCall>();
  return {
    calls,
    async create(req) {
      const pair = await contacts.findPair(req.callerAppUserId, req.calleeAppUserId);
      if (pair?.status !== "accepted") return { ok: false, reason: "not_friends" };
      calls.set(req.callSessionId, { callerAppUserId: req.callerAppUserId, calleeAppUserId: req.calleeAppUserId, callType: req.callType });
      return { ok: true };
    },
    async answer(req) {
      const call = calls.get(req.callSessionId);
      if (!call) return { ok: false, reason: "not_found" };
      call.callType = req.effectiveType;
      // Each side's own token; the IM server never hands one side the other's.
      return {
        ok: true,
        channelId: `demo-${req.callSessionId}`,
        callerToken: `demo-token-caller-${req.callSessionId}`,
        calleeToken: `demo-token-callee-${req.callSessionId}`,
        appId: "demo",
        videoQuality: "sd",
        denoiseEnabled: true,
      };
    },
    async settle(req) {
      calls.delete(req.callSessionId);
      log(`call ${req.callSessionId} ended: ${req.reason}`);
    },
    async upgrade(req) {
      const call = calls.get(req.callSessionId);
      if (!call) return { ok: false, reason: "not_found" };
      call.callType = "video";
      return { ok: true, videoQuality: "sd" };
    },
  };
}
