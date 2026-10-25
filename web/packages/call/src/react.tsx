/**
 * React components for calls: the in-tab call screen (ringing, calling, in
 * a call, ended) and the call buttons for a single chat's header. They need
 * an `<ImKitProvider>` above a `<CallProvider>`, and use the kit's CSS
 * variables.
 */

import { useImKit, useProfile, useSignal } from "@imkit/react";
import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { CallManager, ToneRinger, ringWith, type CallEndReason, type CallMediaEngine, type CallRinger, type CallState, type CallTraceEntry, type CallType, type TabChannel } from "./index.js";

// ------------------------------------------------------------------ strings

export interface CallStrings {
  voiceCall: string;
  videoCall: string;
  incomingVoice: string;
  incomingVideo: string;
  calling: string;
  connecting: string;
  accept: string;
  acceptAsVoice: string;
  decline: string;
  cancel: string;
  hangUp: string;
  mute: string;
  unmute: string;
  switchToVideo: string;
  videoAsked: string;
  videoRequested: string;
  acceptVideo: string;
  declineVideo: string;
  videoDeclined: string;
  someone: string;
  refusedOffline: string;
  refusedBusy: string;
  refusedNotFriends: string;
  refused: string;
  ended: Record<string, string>;
}

export const callEn: CallStrings = {
  voiceCall: "Voice call",
  videoCall: "Video call",
  incomingVoice: "Incoming voice call",
  incomingVideo: "Incoming video call",
  calling: "Calling…",
  connecting: "Connecting…",
  accept: "Answer",
  acceptAsVoice: "Answer with voice",
  decline: "Decline",
  cancel: "Cancel",
  hangUp: "Hang up",
  mute: "Mute",
  unmute: "Unmute",
  switchToVideo: "Switch to video",
  videoAsked: "Waiting for them to turn on video…",
  videoRequested: "They want to switch to video",
  acceptVideo: "Turn on video",
  declineVideo: "Not now",
  videoDeclined: "Video declined",
  someone: "Someone",
  refusedOffline: "They are offline",
  refusedBusy: "They are in another call",
  refusedNotFriends: "You can only call your contacts",
  refused: "The call could not be made",
  ended: {
    user_hangup: "Call ended",
    no_answer: "No answer",
    rejected: "Declined",
    cancelled: "Cancelled",
    disconnected: "Connection lost",
    failed: "Call failed",
    answered_elsewhere: "Answered on another device",
    credit_exhausted: "Out of credit",
    max_duration_reached: "Time limit reached",
    "": "Call ended",
  },
};

export const callZh: CallStrings = {
  voiceCall: "语音通话",
  videoCall: "视频通话",
  incomingVoice: "邀请你语音通话",
  incomingVideo: "邀请你视频通话",
  calling: "正在呼叫…",
  connecting: "正在连接…",
  accept: "接听",
  acceptAsVoice: "以语音接听",
  decline: "拒绝",
  cancel: "取消",
  hangUp: "挂断",
  mute: "静音",
  unmute: "取消静音",
  switchToVideo: "切换到视频",
  videoAsked: "等待对方开启视频…",
  videoRequested: "对方请求切换到视频通话",
  acceptVideo: "开启视频",
  declineVideo: "暂不",
  videoDeclined: "对方拒绝了视频",
  someone: "对方",
  refusedOffline: "对方不在线",
  refusedBusy: "对方正在通话中",
  refusedNotFriends: "只能和好友通话",
  refused: "无法发起通话",
  ended: {
    user_hangup: "通话已结束",
    no_answer: "无人接听",
    rejected: "已拒绝",
    cancelled: "已取消",
    disconnected: "连接中断",
    failed: "通话失败",
    answered_elsewhere: "已在其他设备接听",
    credit_exhausted: "余额不足，通话已结束",
    max_duration_reached: "已达通话时长上限",
    "": "通话已结束",
  },
};

export const callVi: CallStrings = {
  voiceCall: "Gọi thoại",
  videoCall: "Gọi video",
  incomingVoice: "Cuộc gọi thoại đến",
  incomingVideo: "Cuộc gọi video đến",
  calling: "Đang gọi…",
  connecting: "Đang kết nối…",
  accept: "Trả lời",
  acceptAsVoice: "Trả lời bằng thoại",
  decline: "Từ chối",
  cancel: "Hủy",
  hangUp: "Kết thúc",
  mute: "Tắt tiếng",
  unmute: "Bật tiếng",
  switchToVideo: "Chuyển sang video",
  videoAsked: "Đang chờ họ bật video…",
  videoRequested: "Họ muốn chuyển sang gọi video",
  acceptVideo: "Bật video",
  declineVideo: "Để sau",
  videoDeclined: "Họ đã từ chối video",
  someone: "Ai đó",
  refusedOffline: "Họ đang ngoại tuyến",
  refusedBusy: "Họ đang trong cuộc gọi khác",
  refusedNotFriends: "Bạn chỉ có thể gọi cho danh bạ",
  refused: "Không thể thực hiện cuộc gọi",
  ended: {
    user_hangup: "Cuộc gọi đã kết thúc",
    no_answer: "Không trả lời",
    rejected: "Đã từ chối",
    cancelled: "Đã hủy",
    disconnected: "Mất kết nối",
    failed: "Cuộc gọi thất bại",
    answered_elsewhere: "Đã trả lời trên thiết bị khác",
    credit_exhausted: "Hết tín dụng",
    max_duration_reached: "Đã đạt giới hạn thời gian",
    "": "Cuộc gọi đã kết thúc",
  },
};

/** The built-in table for a BCP 47 tag; English when the language is not built in. */
export function callStringsFor(locale: string): CallStrings {
  const lang = locale.toLowerCase().split("-")[0];
  return lang === "zh" ? callZh : lang === "vi" ? callVi : callEn;
}

/** The text for an end reason; unknown reasons get the plain "call ended" (section 16.4). */
export const endedText = (strings: CallStrings, reason: CallEndReason): string => strings.ended[reason] ?? strings.ended[""] ?? "";

/** The text for a refused invite. */
export function refusedText(strings: CallStrings, r: { code: number; reason: string; errCode?: string }): string {
  if (r.reason === "offline") return strings.refusedOffline;
  if (r.reason === "busy" || r.reason === "busy_here") return strings.refusedBusy;
  if (r.reason === "not_friends" || r.errCode === "not_friends") return strings.refusedNotFriends;
  return strings.refused;
}

export const callCss = `
.imkit-call { position: fixed; inset: 0; z-index: 1000; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 14px;
  background: rgb(17 24 39 / .92); color: #fff; font-family: var(--imkit-font); }
.imkit-call-name { font-size: 22px; font-weight: 600; }
.imkit-call-status { opacity: .8; }
.imkit-call-actions { display: flex; gap: 14px; flex-wrap: wrap; justify-content: center; }
.imkit-call-actions button { border: 0; border-radius: 999px; padding: 12px 20px; font: inherit; cursor: pointer; background: rgb(255 255 255 / .16); color: #fff; }
.imkit-call-actions .imkit-call-accept { background: #16a34a; }
.imkit-call-actions .imkit-call-end { background: var(--imkit-danger, #dc2626); }
.imkit-call-videos { position: relative; width: min(720px, 92vw); aspect-ratio: 16 / 9; background: #000; border-radius: 12px; overflow: hidden; }
.imkit-call-remote { position: absolute; inset: 0; }
.imkit-call-local { position: absolute; right: 10px; bottom: 10px; width: 28%; aspect-ratio: 16 / 9; border-radius: 8px; overflow: hidden; background: #111; }
.imkit-call-notice { background: rgb(255 255 255 / .12); border-radius: 10px; padding: 8px 14px; display: flex; gap: 10px; align-items: center; }
`;

/** Puts the module's stylesheet in the page once (next to `<ImKitStyles>`). */
export function CallStyles() {
  return <style data-imkit-call="">{callCss}</style>;
}

// ------------------------------------------------------------------ context

interface CallValue {
  manager: CallManager;
  strings: CallStrings;
}

const CallContext = createContext<CallValue | null>(null);

export interface CallProviderProps {
  /** The RTC adapter; keep it stable across renders. */
  media: CallMediaEngine;
  /** How tabs agree on which takes a call; default a BroadcastChannel per account, null for none. */
  channel?: TabChannel | null;
  strings?: Partial<CallStrings>;
  /** Plays the ring and the ringback; default a `ToneRinger`, null for silence. Keep it stable across renders. */
  ringer?: CallRinger | null;
  /** Each trace entry as it happens, for a debug log; `manager.trace()` keeps the last 100. */
  log?: (entry: CallTraceEntry) => void;
  children: ReactNode;
}

/** Makes calls available below; one per `<ImKitProvider>`. */
export function CallProvider({ media, channel, strings, ringer, log, children }: CallProviderProps) {
  const { engine, locale } = useImKit();
  const logRef = useRef(log);
  useEffect(() => {
    logRef.current = log;
  }, [log]);
  // Built idle and started by the effect: StrictMode builds it twice and drops
  // one, and a dropped one that listened would keep hearing every ring.
  const manager = useMemo(
    () => new CallManager(engine.client, engine.me, { media, autoStart: false, log: (e) => logRef.current?.(e), ...(channel === undefined ? {} : { channel }) }),
    [engine, media, channel],
  );
  const sound = useMemo(() => (ringer === undefined ? new ToneRinger() : ringer), [ringer]);
  // StrictMode runs the cleanup once before mounting again: dispose, then start.
  useEffect(() => {
    manager.start();
    const off = sound ? ringWith(manager, sound) : null;
    return () => {
      off?.();
      manager.dispose();
    };
  }, [manager, sound]);
  const value = useMemo(() => ({ manager, strings: { ...callStringsFor(locale), ...strings } }), [manager, locale, strings]);
  return <CallContext.Provider value={value}>{children}</CallContext.Provider>;
}

export function useCall(): CallValue & { state: CallState } {
  const v = useContext(CallContext);
  if (!v) throw new Error("call components need a <CallProvider>");
  const state = useSignal(v.manager.state);
  return { ...v, state };
}

// ------------------------------------------------------------------ buttons

export interface CallButtonsProps {
  /** The other person's uid. */
  peer: string;
  /** Called when the invite is refused, with a text to show. */
  onRefused?: (text: string) => void;
  className?: string;
}

/** Voice and video call buttons for a single chat's header. */
export function CallButtons({ peer, onRefused, className = "imkit-icon-button" }: CallButtonsProps) {
  const { manager, strings, state } = useCall();
  const busy = state.phase !== "idle" && state.phase !== "ended";
  const call = async (type: CallType) => {
    const r = await manager.invite(peer, type);
    if (!r.ok) onRefused?.(refusedText(strings, r));
  };
  return (
    <>
      <button type="button" className={className} disabled={busy} onClick={() => void call("voice")} aria-label={strings.voiceCall}>
        {strings.voiceCall}
      </button>
      <button type="button" className={className} disabled={busy} onClick={() => void call("video")} aria-label={strings.videoCall}>
        {strings.videoCall}
      </button>
    </>
  );
}

// ------------------------------------------------------------------ the call screen

export interface CallOverlayProps {
  /** Names the person on the caller's side (the callee's uid); default: the host's profile. */
  nameOf?: (uid: string) => string | undefined;
}

/** The call screen over the page while a call rings, runs or has just ended; nothing when idle. */
export function CallOverlay({ nameOf }: CallOverlayProps) {
  const { manager, strings, state } = useCall();
  const peerUid = state.phase === "outgoing" || state.phase === "active" ? state.peer : undefined;
  const profile = useProfile(peerUid);
  const name = (peerUid && (nameOf?.(peerUid) ?? profile?.name)) || ("callerName" in state ? state.callerName : undefined) || strings.someone;
  const elapsed = useElapsed(state.phase === "active" && !state.connecting ? state.since : null);

  if (state.phase === "idle") return null;
  return (
    <div className="imkit-call" role="dialog" aria-label={name} data-call-phase={state.phase}>
      {state.phase === "incoming" && state.callerAvatar && <img src={state.callerAvatar} alt="" width={72} height={72} style={{ borderRadius: "50%" }} />}
      <div className="imkit-call-name">{name}</div>
      <div className="imkit-call-status" data-call-status="">
        {state.phase === "incoming" && (state.callType === "video" ? strings.incomingVideo : strings.incomingVoice)}
        {state.phase === "outgoing" && strings.calling}
        {state.phase === "active" && (state.connecting ? strings.connecting : elapsed)}
        {state.phase === "ended" && endedText(strings, state.reason)}
      </div>
      {state.phase === "active" && state.callType === "video" && <Videos />}
      {state.phase === "active" && state.upgrade === "theirs" && (
        <div className="imkit-call-notice">
          {strings.videoRequested}
          <div className="imkit-call-actions">
            <button type="button" className="imkit-call-accept" onClick={() => void manager.acceptVideo()}>
              {strings.acceptVideo}
            </button>
            <button type="button" onClick={() => void manager.declineVideo()}>
              {strings.declineVideo}
            </button>
          </div>
        </div>
      )}
      {state.phase === "active" && state.upgrade === "mine" && <div className="imkit-call-notice">{strings.videoAsked}</div>}
      {state.phase === "active" && state.upgrade === "declined" && <div className="imkit-call-notice">{strings.videoDeclined}</div>}
      <div className="imkit-call-actions">
        {state.phase === "incoming" && (
          <>
            <button type="button" className="imkit-call-accept" onClick={() => void manager.accept()}>
              {strings.accept}
            </button>
            {state.callType === "video" && (
              <button type="button" onClick={() => void manager.accept(true)}>
                {strings.acceptAsVoice}
              </button>
            )}
            <button type="button" className="imkit-call-end" onClick={() => void manager.reject()}>
              {strings.decline}
            </button>
          </>
        )}
        {state.phase === "outgoing" && (
          <button type="button" className="imkit-call-end" onClick={() => void manager.cancel()}>
            {strings.cancel}
          </button>
        )}
        {state.phase === "active" && (
          <>
            <button type="button" aria-pressed={state.muted} onClick={() => manager.setMuted(!state.muted)}>
              {state.muted ? strings.unmute : strings.mute}
            </button>
            {state.callType === "voice" && !state.upgrade && (
              <button type="button" onClick={() => void manager.requestVideo()}>
                {strings.switchToVideo}
              </button>
            )}
            <button type="button" className="imkit-call-end" onClick={() => void manager.hangup()}>
              {strings.hangUp}
            </button>
          </>
        )}
      </div>
    </div>
  );
}

/** The other side's video, with this side's camera in the corner, through the adapter. */
function Videos() {
  const { manager } = useCall();
  const media = manager.media;
  const remote = useRef<HTMLDivElement>(null);
  const local = useRef<HTMLDivElement>(null);
  useEffect(() => {
    media.attachRemoteVideo?.(remote.current);
    media.attachLocalVideo?.(local.current);
    return () => {
      media.attachRemoteVideo?.(null);
      media.attachLocalVideo?.(null);
    };
  }, [media]);
  return (
    <div className="imkit-call-videos">
      <div className="imkit-call-remote" ref={remote} />
      <div className="imkit-call-local" ref={local} />
    </div>
  );
}

/** "m:ss" since `since`, ticking every second; "" when null. */
function useElapsed(since: number | null): string {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (since === null) return;
    const t = setInterval(() => setNow(Date.now()), 1_000);
    return () => clearInterval(t);
  }, [since]);
  if (since === null) return "";
  const s = Math.max(0, Math.floor((now - since) / 1000));
  const h = Math.floor(s / 3600);
  const mm = String(Math.floor((s % 3600) / 60)).padStart(h ? 2 : 1, "0");
  const ss = String(s % 60).padStart(2, "0");
  return h ? `${h}:${mm}:${ss}` : `${mm}:${ss}`;
}
