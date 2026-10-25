import { isGroupConversation, type ChatMessage, type ConversationId, type ConversationSummary, type VoiceClip, VoiceRecorder } from "@imkit/client";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { shortUid, useConnectionState, useConversation, useConversations, useImKit, useMessages, usePresence, useProfile, useSignal, useTyping } from "./context.js";
import type { MessageAction, MessageScope } from "./registry.js";
import { format, type ImStrings } from "./strings.js";
import { imkitCss } from "./theme.js";

/** Puts the kit's stylesheet in the page once. */
export function ImKitStyles() {
  return <style data-imkit="">{imkitCss}</style>;
}

// ------------------------------------------------------------------ small parts

export function Avatar({ uid, name, url, size = 40, online }: { uid: string; name?: string; url?: string; size?: number; online?: boolean }) {
  const label = (name || shortUid(uid)).trim();
  const style: CSSProperties = { width: size, height: size, fontSize: size * 0.42, background: url ? undefined : colorFor(uid) };
  return (
    <span className="imkit-avatar" style={style} aria-hidden="true">
      {url ? <img src={url} alt="" /> : Array.from(label)[0]?.toUpperCase()}
      {online && <span className="imkit-dot" />}
    </span>
  );
}

function colorFor(id: string): string {
  let h = 0;
  for (const ch of id) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return `hsl(${h % 360} 55% 48%)`;
}

const DAY = 86_400_000;

function startOfDay(ts: number): number {
  const d = new Date(ts);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

export function dayLabel(ts: number, s: ImStrings, now = Date.now()): string {
  const diff = Math.round((startOfDay(now) - startOfDay(ts)) / DAY);
  if (diff === 0) return s.today;
  if (diff === 1) return s.yesterday;
  return new Date(ts).toLocaleDateString(undefined, { year: diff > 300 ? "numeric" : undefined, month: "short", day: "numeric" });
}

export function listTime(ts: number, s: ImStrings, now = Date.now()): string {
  if (!ts) return "";
  if (now - ts < 60_000) return s.justNow;
  if (startOfDay(ts) === startOfDay(now)) return new Date(ts).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  return dayLabel(ts, s, now);
}

function lastSeen(lastSeenAt: number, s: ImStrings, now = Date.now()): string {
  const min = Math.max(1, Math.round((now - lastSeenAt) / 60_000));
  if (min < 60) return format(s.lastSeenMinutes, min);
  if (min < 60 * 24) return format(s.lastSeenHours, Math.round(min / 60));
  return format(s.lastSeenDays, Math.round(min / 1440));
}

/** "No connection" while the client is not logged in. */
export function ConnectionBanner() {
  const { strings } = useImKit();
  const state = useConnectionState();
  if (state.kind === "connected" || state.kind === "idle") return null;
  return <div className="imkit-banner" role="status">{state.kind === "connecting" ? strings.connecting : strings.offlineBanner}</div>;
}

// ------------------------------------------------------------------ conversation list

function ConversationTitle({ c }: { c: ConversationSummary }) {
  const profile = useProfile(c.isGroup ? undefined : c.id);
  return <>{c.isGroup ? (c.title ?? shortUid(c.id)) : (profile?.name ?? c.title ?? shortUid(c.id))}</>;
}

function ConversationRow({ c, active, onOpen }: { c: ConversationSummary; active: boolean; onOpen(id: ConversationId): void }) {
  const { strings, types } = useImKit();
  const profile = useProfile(c.isGroup ? undefined : c.id);
  const sender = useProfile(c.isGroup && c.lastMessage && c.lastMessage.status === "received" ? c.lastMessage.from : undefined);
  const presence = usePresence(c.isGroup ? undefined : c.id);
  let preview = c.lastMessage ? types.preview(c.lastMessage, strings) : "";
  if (c.isGroup && c.lastMessage && sender) preview = format(strings.senderPreview, sender.name, preview);
  return (
    <button type="button" className="imkit-list-item" aria-current={active} onClick={() => onOpen(c.id)} data-conversation={c.id}>
      <Avatar uid={c.id} name={c.isGroup ? c.title : profile?.name} url={profile?.avatarUrl} online={presence === "online"} />
      <span className="imkit-list-main">
        <span className="imkit-list-row">
          <span className="imkit-list-title"><ConversationTitle c={c} /></span>
          <span className="imkit-list-time">{listTime(c.lastMessage?.ts ?? c.updatedAt, strings)}</span>
        </span>
        <span className="imkit-list-row">
          <span className="imkit-list-preview">
            {c.draft ? <><span className="imkit-list-draft">{strings.draft}</span> {c.draft}</> : c.left ? strings.removedFromGroup : preview}
          </span>
          {c.unread > 0 && <span className={"imkit-badge" + (c.muted ? " imkit-badge-muted" : "")}>{c.unread > 99 ? "99+" : c.unread}</span>}
        </span>
      </span>
    </button>
  );
}

export interface ConversationListProps {
  onOpen(conversation: ConversationId): void;
  /** The conversation shown next to the list, highlighted. */
  activeConversation?: ConversationId | null;
  /** Which conversations to show; default all but groups I left and empty single chats. */
  filter?: (c: ConversationSummary) => boolean;
}

export function ConversationList({ onOpen, activeConversation, filter }: ConversationListProps) {
  const { strings } = useImKit();
  const all = useConversations();
  const shown = all.filter(filter ?? ((c) => !(c.isGroup && c.left && !c.lastMessage) && (c.isGroup || !!c.lastMessage || !!c.draft)));
  return (
    <div className="imkit-list" role="list" aria-label={strings.chats}>
      {shown.length === 0 ? (
        <div className="imkit-list-empty">{strings.noConversations}</div>
      ) : (
        shown.map((c) => <ConversationRow key={c.id} c={c} active={c.id === activeConversation} onOpen={onOpen} />)
      )}
    </div>
  );
}

// ------------------------------------------------------------------ chat

export interface ChatScreenProps {
  conversation: ConversationId;
  onBack?: () => void;
  /** Extra buttons on the right of the header. */
  headerActions?: ReactNode;
}

export function ChatScreen({ conversation, onBack, headerActions }: ChatScreenProps) {
  const { engine, strings } = useImKit();
  const summary = useConversation(conversation);
  const isGroup = isGroupConversation(conversation);
  const profile = useProfile(isGroup ? undefined : conversation);
  const presence = usePresence(isGroup ? undefined : conversation);
  const typing = useTyping(conversation);
  const typer = useProfile(typing[0]);
  const connection = useConnectionState();

  // Opening a chat subscribes to it: pushes and presence follow subscriptions (section 7).
  useEffect(() => {
    if (connection.kind === "connected") void engine.subscribe(conversation).catch(() => {});
  }, [engine, conversation, connection.kind]);

  let subtitle = "";
  if (typing.length > 0) subtitle = isGroup && typer ? format(strings.typingNamed, typer.name) : strings.typing;
  else if (!isGroup && presence === "online") subtitle = strings.online;
  else if (!isGroup && presence === "offline") {
    const seen = engine.presence.record(conversation)?.lastSeenAt ?? 0;
    if (seen > 0) subtitle = lastSeen(seen, strings);
  }
  const title = isGroup ? (summary?.title ?? shortUid(conversation)) : (profile?.name ?? shortUid(conversation));

  return (
    <section className="imkit-chat" aria-label={title} style={{ position: "relative" }}>
      <header className="imkit-chat-header">
        {onBack && (
          <button type="button" className="imkit-icon-button" onClick={onBack} aria-label={strings.back}>
            ←
          </button>
        )}
        <Avatar uid={conversation} name={title} url={profile?.avatarUrl} size={36} online={presence === "online"} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div className="imkit-chat-title">{title}</div>
          <div className="imkit-chat-subtitle" aria-live="polite">{subtitle}</div>
        </div>
        {headerActions}
      </header>
      <ConnectionBanner />
      <MessageList conversation={conversation} summary={summary} />
      {summary?.left ? <div className="imkit-banner">{strings.removedFromGroup}</div> : <Composer conversation={conversation} />}
    </section>
  );
}

function MessageList({ conversation, summary }: { conversation: ConversationId; summary: ConversationSummary | undefined }) {
  const { engine, strings, host } = useImKit();
  const messages = useMessages(conversation);
  const isGroup = isGroupConversation(conversation);
  const box = useRef<HTMLDivElement | null>(null);
  const atBottom = useRef(true);
  const loadingOlder = useRef(false);
  const [viewer, setViewer] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ message: ChatMessage; x: number; y: number } | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const cursors = useSignal(useMemo(() => engine.readCursors(conversation), [engine, conversation]));

  // The "new messages" line goes above the first unread message as it was when the chat opened.
  const [divider] = useState(() => summary?.lastReadSeq ?? 0);
  const firstUnread = messages.find((m) => m.seq !== null && m.seq > divider && m.from !== engine.me)?.seq ?? null;

  const scope: MessageScope = useMemo(
    () => ({ isMine: false, strings, resolveUrl: (u) => (host.resolveMediaUrl ? host.resolveMediaUrl(u) : u || undefined), openImage: setViewer }),
    [strings, host],
  );

  // Stay at the bottom as messages arrive, unless the user scrolled up.
  const lastKey = messages.length ? messages[messages.length - 1]! : null;
  useLayoutEffect(() => {
    const el = box.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [lastKey, messages.length]);

  // What is on screen is read (the page must be visible).
  useEffect(() => {
    const mark = () => {
      if (atBottom.current && document.visibilityState === "visible") void engine.markRead(conversation);
    };
    mark();
    document.addEventListener("visibilitychange", mark);
    return () => document.removeEventListener("visibilitychange", mark);
  }, [engine, conversation, lastKey]);

  const onScroll = useCallback(async () => {
    const el = box.current;
    if (!el) return;
    atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (atBottom.current) void engine.markRead(conversation);
    if (el.scrollTop < 60 && !loadingOlder.current) {
      loadingOlder.current = true;
      const before = el.scrollHeight;
      try {
        const n = await engine.loadOlder(conversation);
        if (n > 0) requestAnimationFrame(() => (el.scrollTop += el.scrollHeight - before));
      } catch {
        setToast(strings.loadOlderFailed);
      } finally {
        loadingOlder.current = false;
      }
    }
  }, [engine, conversation, strings]);

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 2500);
    return () => clearTimeout(t);
  }, [toast]);

  const run = async (action: MessageAction, m: ChatMessage) => {
    setMenu(null);
    try {
      if (action === "copy") await navigator.clipboard?.writeText(m.content);
      else if (action === "recall") await engine.recall(m).catch(() => setToast(strings.recallFailed));
      else if (action === "retry") await engine.retry(m);
      else if (action === "discard") await engine.discard(m);
    } catch {
      setToast(strings.actionFailed);
    }
  };

  // Own messages show their state; a read mark only on the newest one the peer has read.
  const lastOwnSeq = [...messages].reverse().find((m) => m.from === engine.me && m.seq !== null)?.seq ?? null;

  const rows: ReactNode[] = [];
  let day = -1;
  for (const m of messages) {
    const d = startOfDay(m.ts);
    if (d !== day) {
      day = d;
      rows.push(<div key={"d" + d} className="imkit-divider">{dayLabel(m.ts, strings)}</div>);
    }
    if (m.seq !== null && m.seq === firstUnread) rows.push(<div key="new" className="imkit-divider imkit-divider-new">{strings.newMessages}</div>);
    let status: ReactNode = null;
    if (m.from === engine.me) {
      if (m.status === "sending") status = <span className="imkit-status">{strings.sending}</span>;
      else if (m.status === "failed")
        status = (
          <button type="button" className="imkit-status imkit-status-failed" onClick={() => void engine.retry(m)}>
            {strings.sendFailedRetry}
          </button>
        );
      else if (m.seq === lastOwnSeq) {
        if (!isGroup && (summary?.peerReadSeq ?? 0) >= m.seq!) status = <span className="imkit-status">{strings.read}</span>;
        else if (isGroup) {
          const readers = [...cursors].filter(([uid, seq]) => uid !== engine.me && seq >= m.seq!).length;
          if (readers > 0) status = <span className="imkit-status">{format(strings.readByCount, readers)}</span>;
        }
      }
    }
    rows.push(
      <MessageRow
        key={m.seq !== null ? "s" + m.seq : "l" + m.localId}
        message={m}
        mine={m.from === engine.me}
        showSender={isGroup}
        scope={scope}
        status={status}
        onMenu={(x, y) => setMenu({ message: m, x, y })}
      />,
    );
  }

  return (
    <>
      <div className="imkit-messages" ref={box} onScroll={() => void onScroll()} role="log" aria-live="polite">
        {rows}
      </div>
      {menu && <MessageMenu message={menu.message} x={menu.x} y={menu.y} onAction={run} onClose={() => setMenu(null)} />}
      {viewer && (
        <div className="imkit-viewer" onClick={() => setViewer(null)} role="dialog">
          <img src={viewer} alt="" />
        </div>
      )}
      {toast && <div className="imkit-toast" role="alert">{toast}</div>}
    </>
  );
}

function MessageRow({ message, mine, showSender, scope, status, onMenu }: { message: ChatMessage; mine: boolean; showSender: boolean; scope: MessageScope; status: ReactNode; onMenu(x: number, y: number): void }) {
  const { types, host } = useImKit();
  const sender = useProfile(showSender && !mine ? message.from : undefined);
  const press = useLongPress(onMenu);
  const type = types.typeOf(message);
  const content = type.render(message, { ...scope, isMine: mine });
  if (type.name === "system") return <>{content}</>;
  return (
    <div className={"imkit-row" + (mine ? " imkit-row-mine" : "")} data-seq={message.seq ?? undefined} data-type={type.name}>
      {!mine && showSender ? (
        <span onClick={() => host.onOpenProfile?.(message.from)}>
          <Avatar uid={message.from} name={sender?.name} url={sender?.avatarUrl} size={32} />
        </span>
      ) : null}
      <div className="imkit-row-body">
        {!mine && showSender && <span className="imkit-sender">{sender?.name ?? shortUid(message.from)}</span>}
        <div
          className={type.framed === false ? "imkit-card" : "imkit-bubble" + (message.status === "failed" ? " imkit-bubble-failed" : "")}
          onContextMenu={(e) => {
            e.preventDefault();
            onMenu(e.clientX, e.clientY);
          }}
          {...press}
        >
          {message.forwardFromMsgId ? <span className="imkit-forwarded">{scope.strings.forwarded}</span> : null}
          {content}
        </div>
        {status}
      </div>
    </div>
  );
}

/** A touch held for half a second opens the menu, like a right click. */
function useLongPress(onMenu: (x: number, y: number) => void) {
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const clear = () => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
  };
  return {
    onTouchStart: (e: React.TouchEvent) => {
      const t = e.touches[0];
      if (!t) return;
      const { clientX, clientY } = t;
      clear();
      timer.current = setTimeout(() => onMenu(clientX, clientY), 500);
    },
    onTouchEnd: clear,
    onTouchMove: clear,
  };
}

function MessageMenu({ message, x, y, onAction, onClose }: { message: ChatMessage; x: number; y: number; onAction(a: MessageAction, m: ChatMessage): void; onClose(): void }) {
  const { types, strings, engine } = useImKit();
  const mine = message.from === engine.me;
  const actions = (types.typeOf(message).actions?.(message) ?? []).filter((a) => mine || a === "copy");
  useEffect(() => {
    const close = () => onClose();
    window.addEventListener("click", close);
    window.addEventListener("keydown", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("keydown", close);
    };
  }, [onClose]);
  if (actions.length === 0) return null;
  const label: Record<MessageAction, string> = { copy: strings.actionCopy, recall: strings.actionRecall, retry: strings.actionRetry, discard: strings.actionDiscard };
  return (
    <div className="imkit-menu" style={{ left: Math.min(x, window.innerWidth - 160), top: Math.min(y, window.innerHeight - 160) }} role="menu">
      {actions.map((a) => (
        <button key={a} type="button" role="menuitem" onClick={() => onAction(a, message)}>
          {label[a]}
        </button>
      ))}
    </div>
  );
}

// ------------------------------------------------------------------ composer

const TYPING_EVERY_MS = 4_000;
const CANCEL_LIFT_PX = 60;

function Composer({ conversation }: { conversation: ConversationId }) {
  const { engine, strings, host } = useImKit();
  const summary = useConversation(conversation);
  const [text, setText] = useState(summary?.draft ?? "");
  const [mode, setMode] = useState<"text" | "voice">("text");
  const [toast, setToast] = useState<string | null>(null);
  const lastTyping = useRef(0);
  const file = useRef<HTMLInputElement | null>(null);

  // Keep the draft when the chat closes.
  const latest = useRef(text);
  latest.current = text;
  useEffect(() => () => void engine.setDraft(conversation, latest.current.trim() ? latest.current : undefined), [engine, conversation]);

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 2500);
    return () => clearTimeout(t);
  }, [toast]);

  const send = () => {
    const body = text.trim();
    if (!body) return;
    setText("");
    void engine.send(conversation, { content: body });
  };

  const onInput = (v: string) => {
    setText(v);
    const now = Date.now();
    if (v && now - lastTyping.current > TYPING_EVERY_MS) {
      lastTyping.current = now;
      engine.sendTyping(conversation);
    }
  };

  const onFile = async (f: File | undefined) => {
    if (!f || !host.media) return;
    const r = await host.media.sendFile(conversation, f);
    if (r.kind === "tooLarge") setToast(strings.tooLarge);
    else if (r.kind === "failed") setToast(strings.sendMediaFailed);
  };

  const onVoice = async (clip: VoiceClip) => {
    if (!host.media) return;
    const r = await host.media.sendVoice(conversation, clip);
    if (r.kind === "failed") setToast(strings.sendMediaFailed);
  };

  return (
    <div className="imkit-composer">
      {host.media && (
        <button type="button" className="imkit-icon-button" onClick={() => setMode(mode === "text" ? "voice" : "text")} aria-label={mode === "text" ? strings.voiceInput : strings.keyboard}>
          {mode === "text" ? "🎤" : "⌨"}
        </button>
      )}
      {mode === "voice" ? (
        <HoldToTalk onClip={onVoice} onTooShort={() => setToast(strings.recordTooShort)} onDenied={() => setToast(strings.micPermission)} />
      ) : (
        <textarea
          className="imkit-input"
          rows={1}
          value={text}
          placeholder={strings.typeMessage}
          aria-label={strings.typeMessage}
          onChange={(e) => onInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
              e.preventDefault();
              send();
            }
          }}
        />
      )}
      {host.media && (
        <>
          <button type="button" className="imkit-icon-button" onClick={() => file.current?.click()} aria-label={strings.attach}>
            ＋
          </button>
          <input ref={file} type="file" hidden onChange={(e) => void onFile(e.target.files?.[0]).finally(() => (e.target.value = ""))} />
        </>
      )}
      {mode === "text" && (
        <button type="button" className="imkit-send" onClick={send} disabled={!text.trim()}>
          {strings.send}
        </button>
      )}
      {toast && <div className="imkit-toast" role="alert">{toast}</div>}
    </div>
  );
}

/**
 * Press and hold to record, release to send, slide up to cancel: one
 * pointer gesture from down to up (the pointer is captured, so moving does
 * not end it early).
 */
export function HoldToTalk({ onClip, onTooShort, onDenied, recorder: given }: { onClip(clip: VoiceClip): void; onTooShort?(): void; onDenied?(): void; recorder?: VoiceRecorder }) {
  const { strings } = useImKit();
  const recorder = useMemo(() => given ?? new VoiceRecorder(), [given]);
  const state = useSignal(recorder.state);
  const [cancelling, setCancelling] = useState(false);
  const startY = useRef(0);
  const [seconds, setSeconds] = useState(0);
  const pressed = useRef(false);

  useEffect(() => {
    if (state.kind !== "recording") return;
    const t = setInterval(() => setSeconds(Math.floor((Date.now() - state.startedAt) / 1000)), 250);
    return () => clearInterval(t);
  }, [state]);

  const recording = state.kind === "recording";
  return (
    <>
      <button
        type="button"
        className={"imkit-talk" + (recording ? (cancelling ? " imkit-talk-cancel" : " imkit-talk-active") : "")}
        onPointerDown={async (e) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          pressed.current = true;
          startY.current = e.clientY;
          setCancelling(false);
          setSeconds(0);
          const ok = await recorder.start();
          if (!ok) {
            onDenied?.();
            return;
          }
          if (!pressed.current) recorder.cancel(); // released while the permission prompt was up
        }}
        onPointerMove={(e) => {
          if (recording) setCancelling(startY.current - e.clientY > CANCEL_LIFT_PX);
        }}
        onPointerUp={async () => {
          pressed.current = false;
          if (!recording) return;
          if (cancelling) {
            recorder.cancel();
            return;
          }
          const clip = await recorder.stop(1);
          if (clip) onClip(clip);
          else onTooShort?.();
        }}
        onPointerCancel={() => {
          pressed.current = false;
          recorder.cancel();
        }}
      >
        {recording ? (cancelling ? strings.releaseToCancel : strings.releaseToSend) : strings.holdToTalk}
      </button>
      {recording && (
        <div className="imkit-overlay">
          <div className={"imkit-overlay-card" + (cancelling ? " imkit-overlay-cancel" : "")}>
            <div style={{ fontSize: 22 }}>{format(strings.voiceSeconds, seconds)}</div>
            <div style={{ fontSize: 13, marginTop: 6 }}>{cancelling ? strings.releaseToCancel : strings.swipeUpCancel}</div>
          </div>
        </div>
      )}
    </>
  );
}

/** A message bubble on its own, for previews and tests. */
export function MessageBubble({ message, mine = false }: { message: ChatMessage; mine?: boolean }) {
  const { strings, host } = useImKit();
  const scope: MessageScope = { isMine: mine, strings, resolveUrl: (u) => (host.resolveMediaUrl ? host.resolveMediaUrl(u) : u || undefined), openImage: () => {} };
  return <MessageRow message={message} mine={mine} showSender={false} scope={scope} status={null} onMenu={() => {}} />;
}

