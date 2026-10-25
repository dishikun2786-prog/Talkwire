import { isGroupConversation, isRecalled, type ChatMessage, type ConversationId, type ConversationSummary, type VoiceClip, VoiceRecorder } from "@imkit/client";
import { p2pTopic } from "@imkit/protocol";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { shortUid, useConnectionState, useConversation, useConversations, useImKit, useMessages, usePresence, useProfile, useProfilesVersion, useSignal, useTyping } from "./context.js";
import { MESSAGE_ACTION_ORDER, replyExtra, replyRefOf, type MessageAction, type MessageScope, type ReplyRef } from "./registry.js";
import { ConfirmDialog, Dialog, PopupMenu } from "./dialogs.js";
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

function ConversationRow({ c, active, onOpen, onMenu }: { c: ConversationSummary; active: boolean; onOpen(id: ConversationId): void; onMenu(x: number, y: number): void }) {
  const { strings, types } = useImKit();
  const profile = useProfile(c.isGroup ? undefined : c.id);
  const sender = useProfile(c.isGroup && c.lastMessage && c.lastMessage.status === "received" ? c.lastMessage.from : undefined);
  const presence = usePresence(c.isGroup ? undefined : c.id);
  let preview = c.lastMessage ? types.preview(c.lastMessage, strings) : "";
  if (c.isGroup && c.lastMessage && sender) preview = format(strings.senderPreview, sender.name, preview);
  const press = useLongPress(onMenu);
  return (
    <button
      type="button"
      className={"imkit-list-item" + (c.pinned ? " imkit-list-item-pinned" : "")}
      aria-current={active}
      onClick={() => onOpen(c.id)}
      onContextMenu={(e) => {
        e.preventDefault();
        onMenu(e.clientX, e.clientY);
      }}
      {...press}
      data-conversation={c.id}
    >
      <Avatar uid={c.id} name={c.isGroup ? c.title : profile?.name} url={profile?.avatarUrl} online={presence === "online"} />
      <span className="imkit-list-main">
        <span className="imkit-list-row">
          <span className="imkit-list-title"><ConversationTitle c={c} /></span>
          {c.muted && <span className="imkit-list-muted" role="img" aria-label={strings.mute}>🔕</span>}
          <span style={{ flex: 1 }} />
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

/** `text` around the first match of `query`, the match marked; a long lead is cut. */
export function snippet(text: string, query: string, lead = 16): { before: string; match: string; after: string } {
  const flat = text.replace(/\s+/g, " ");
  const at = flat.toLowerCase().indexOf(query.toLowerCase());
  if (at < 0) return { before: flat, match: "", after: "" };
  const start = Math.max(0, at - lead);
  return { before: (start > 0 ? "…" : "") + flat.slice(start, at), match: flat.slice(at, at + query.length), after: flat.slice(at + query.length) };
}

/** A message found by search: the chat it is in, the text around the match. Opens the chat. */
function SearchHitRow({ m, query, onOpen }: { m: ChatMessage; query: string; onOpen(id: ConversationId): void }) {
  const { engine, strings } = useImKit();
  const c = useConversation(m.conversation);
  const isGroup = isGroupConversation(m.conversation);
  const peer = useProfile(isGroup ? undefined : m.conversation);
  const sender = useProfile(isGroup && m.from !== engine.me ? m.from : undefined);
  const title = isGroup ? (c?.title ?? shortUid(m.conversation)) : (peer?.name ?? c?.title ?? shortUid(m.conversation));
  const s = snippet(m.content, query);
  return (
    <button type="button" role="listitem" className="imkit-list-item imkit-search-hit" data-hit={`${m.conversation}:${m.seq ?? m.localId}`} onClick={() => onOpen(m.conversation)}>
      <Avatar uid={m.conversation} name={title} url={peer?.avatarUrl} />
      <span className="imkit-list-main">
        <span className="imkit-list-row">
          <span className="imkit-list-title">{title}</span>
          <span style={{ flex: 1 }} />
          <span className="imkit-list-time">{listTime(m.ts, strings)}</span>
        </span>
        <span className="imkit-list-row">
          <span className="imkit-list-preview">
            {sender && <>{sender.name}: </>}
            {s.before}
            <mark>{s.match}</mark>
            {s.after}
          </span>
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
  /** A search box over chat names and the messages stored on this device; default true. */
  searchable?: boolean;
}

export function ConversationList({ onOpen, activeConversation, filter, searchable = true }: ConversationListProps) {
  const { engine, strings, profiles } = useImKit();
  const all = useConversations();
  const shown = all.filter(filter ?? ((c) => !(c.isGroup && c.left && !c.lastMessage) && (c.isGroup || !!c.lastMessage || !!c.draft)));
  // Right click or long press: pin, mute, delete (this device only).
  const [menu, setMenu] = useState<{ c: ConversationSummary; x: number; y: number } | null>(null);
  const [deleting, setDeleting] = useState<ConversationSummary | null>(null);
  const [query, setQuery] = useState("");
  const q = query.trim();
  useProfilesVersion();
  const nameOf = (c: ConversationSummary) => (c.isGroup ? (c.title ?? "") : (profiles.peek(c.id)?.name ?? c.title ?? ""));
  const chatHits = q ? shown.filter((c) => nameOf(c).toLowerCase().includes(q.toLowerCase())) : [];
  // Message hits come from the store, a moment after typing stops; null while looking.
  const [hits, setHits] = useState<{ q: string; messages: ChatMessage[] } | null>(null);
  useEffect(() => {
    if (!q) return;
    let live = true;
    const t = setTimeout(() => {
      void engine.searchMessages(q).then(
        (messages) => live && setHits({ q, messages }),
        () => live && setHits({ q, messages: [] }),
      );
    }, 250);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [engine, q]);
  const messageHits = hits && hits.q === q ? hits.messages : null;
  return (
    <div className="imkit-list">
      {searchable && (
        <div className="imkit-list-searchbar">
          <input className="imkit-field" type="search" aria-label={strings.searchChats} placeholder={strings.search} value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
      )}
      {q ? (
        <div role="list" aria-label={strings.search}>
          {chatHits.length > 0 && <div className="imkit-list-header">{strings.chats}</div>}
          {chatHits.map((c) => <ConversationRow key={c.id} c={c} active={c.id === activeConversation} onOpen={onOpen} onMenu={(x, y) => setMenu({ c, x, y })} />)}
          {messageHits && messageHits.length > 0 && <div className="imkit-list-header">{strings.searchMessages}</div>}
          {messageHits?.map((m) => <SearchHitRow key={`${m.conversation}:${m.seq ?? m.localId}`} m={m} query={q} onOpen={onOpen} />)}
          {messageHits && messageHits.length === 0 && chatHits.length === 0 && <div className="imkit-list-empty">{strings.noResults}</div>}
        </div>
      ) : (
        <div role="list" aria-label={strings.chats}>
          {shown.length === 0 ? (
            <div className="imkit-list-empty">{strings.noConversations}</div>
          ) : (
            shown.map((c) => <ConversationRow key={c.id} c={c} active={c.id === activeConversation} onOpen={onOpen} onMenu={(x, y) => setMenu({ c, x, y })} />)
          )}
        </div>
      )}
      {menu && (
        <PopupMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)}>
          <button type="button" role="menuitem" onClick={() => { setMenu(null); void engine.setPinned(menu.c.id, !menu.c.pinned); }}>
            {menu.c.pinned ? strings.unpin : strings.pin}
          </button>
          <button type="button" role="menuitem" onClick={() => { setMenu(null); void engine.setMuted(menu.c.id, !menu.c.muted); }}>
            {menu.c.muted ? strings.unmute : strings.mute}
          </button>
          <button type="button" role="menuitem" className="imkit-danger-text" onClick={() => { setMenu(null); setDeleting(menu.c); }}>
            {strings.deleteChat}
          </button>
        </PopupMenu>
      )}
      {deleting && <ConfirmDialog message={strings.deleteChatConfirm} action={strings.deleteChat} onClose={() => setDeleting(null)} onConfirm={() => void engine.deleteConversation(deleting.id)} />}
    </div>
  );
}

// ------------------------------------------------------------------ chat

export interface ChatScreenProps {
  conversation: ConversationId;
  onBack?: () => void;
  /** Extra buttons on the right of the header. */
  headerActions?: ReactNode;
  /** Opens the conversation's settings (members, pin, mute, clear); without it there is no info button. */
  onOpenInfo?: () => void;
}

export function ChatScreen({ conversation, onBack, headerActions, onOpenInfo }: ChatScreenProps) {
  const { engine, strings } = useImKit();
  const [replyTo, setReplyTo] = useState<ReplyRef | null>(null);
  // Seqs of the picked messages while selecting several; null when not selecting.
  const [selection, setSelection] = useState<ReadonlySet<number> | null>(null);
  const [forwarding, setForwarding] = useState<{ seqs: number[] } | null>(null);
  const [toast, setToast] = useState<string | null>(null);
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

  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 2500);
    return () => clearTimeout(t);
  }, [toast]);

  // The server knows a single chat by its p2p topic, not by the peer's uid.
  const topicOf = (c: ConversationId) => (isGroupConversation(c) ? c : p2pTopic(engine.me, c));
  const forward = async (targets: ConversationId[]) => {
    const job = forwarding;
    setForwarding(null);
    if (!job || targets.length === 0) return;
    try {
      if (job.seqs.length === 1) {
        // One message to many chats.
        const results = await engine.client.forwardBroadcast(topicOf(conversation), job.seqs[0]!, targets.map(topicOf));
        const failed = targets.length - results.filter((r) => r.ok).length;
        setToast(failed === 0 ? strings.forwardedOk : format(strings.forwardFailed, failed));
      } else {
        // Several messages, in order, into one chat.
        const r = await engine.client.forwardBundle(topicOf(conversation), job.seqs, topicOf(targets[0]!));
        setToast(r.ok ? strings.forwardedOk : strings.actionFailed);
      }
    } catch {
      setToast(strings.actionFailed);
    }
  };

  const toggle = (seq: number) =>
    setSelection((sel) => {
      if (!sel) return sel;
      const next = new Set(sel);
      if (next.has(seq)) next.delete(seq);
      else next.add(seq);
      return next;
    });

  return (
    <section className="imkit-chat" aria-label={title} style={{ position: "relative" }}>
      {selection ? (
        <header className="imkit-chat-header imkit-select-bar">
          <button type="button" className="imkit-icon-button" onClick={() => setSelection(null)} aria-label={strings.cancel}>
            ✕
          </button>
          <div className="imkit-chat-title" style={{ flex: 1 }}>{format(strings.selectedCount, selection.size)}</div>
          <button
            type="button"
            className="imkit-text-button"
            disabled={selection.size === 0}
            onClick={() => {
              const seqs = [...selection].sort((a, b) => a - b);
              setSelection(null);
              setForwarding({ seqs });
            }}
          >
            {strings.actionForward}
          </button>
        </header>
      ) : (
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
        {onOpenInfo && (
          <button type="button" className="imkit-icon-button" onClick={onOpenInfo} aria-label={isGroup ? strings.groupInfo : strings.chatInfo}>
            ⋯
          </button>
        )}
      </header>
      )}
      <ConnectionBanner />
      <MessageList
        conversation={conversation}
        summary={summary}
        selection={selection}
        onToggle={toggle}
        onReply={setReplyTo}
        onForward={(seq) => setForwarding({ seqs: [seq] })}
        onSelect={(seq) => setSelection(new Set([seq]))}
      />
      {summary?.left ? (
        <div className="imkit-banner">{strings.removedFromGroup}</div>
      ) : selection ? null : (
        <Composer conversation={conversation} replyTo={replyTo} onCancelReply={() => setReplyTo(null)} />
      )}
      {forwarding && <ForwardDialog multiple={forwarding.seqs.length === 1} exclude={conversation} onConfirm={(targets) => void forward(targets)} onClose={() => setForwarding(null)} />}
      {toast && <div className="imkit-toast" role="alert">{toast}</div>}
    </section>
  );
}

/**
 * Picks where to forward. With `multiple`, several chats and a send button
 * (one message to many chats); otherwise a click picks the one chat
 * (several messages bundled into one chat).
 */
function ForwardDialog({ multiple, exclude, onConfirm, onClose }: { multiple: boolean; exclude?: ConversationId; onConfirm(targets: ConversationId[]): void; onClose(): void }) {
  const { strings } = useImKit();
  const targets = useConversations().filter((c) => !c.left && c.id !== exclude);
  const [picked, setPicked] = useState<ReadonlySet<ConversationId>>(new Set());
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="imkit-dialog" role="dialog" aria-modal="true" aria-label={strings.forwardTo} onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="imkit-dialog-card">
        <h3 className="imkit-dialog-title">{strings.forwardTo}</h3>
        <div className="imkit-pick-list" role="list">
          {targets.length === 0 ? (
            <div className="imkit-list-empty">{strings.noChats}</div>
          ) : (
            targets.map((c) =>
              multiple ? (
                <label key={c.id} className="imkit-info-row" role="listitem" data-target={c.id}>
                  <input
                    type="checkbox"
                    checked={picked.has(c.id)}
                    onChange={(e) => {
                      const next = new Set(picked);
                      if (e.target.checked) next.add(c.id);
                      else next.delete(c.id);
                      setPicked(next);
                    }}
                  />
                  <TargetRow c={c} />
                </label>
              ) : (
                <button key={c.id} type="button" className="imkit-info-row" role="listitem" data-target={c.id} onClick={() => onConfirm([c.id])}>
                  <TargetRow c={c} />
                </button>
              ),
            )
          )}
        </div>
        <div className="imkit-dialog-actions">
          <button type="button" className="imkit-text-button" onClick={onClose}>{strings.cancel}</button>
          {multiple && (
            <button type="button" className="imkit-send" disabled={picked.size === 0} onClick={() => onConfirm(targets.filter((c) => picked.has(c.id)).map((c) => c.id))}>
              {format(strings.sendCount, picked.size)}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function TargetRow({ c }: { c: ConversationSummary }) {
  const { host } = useImKit();
  const profile = useProfile(c.isGroup ? undefined : c.id);
  const url = c.isGroup ? undefined : host.resolveMediaUrl ? host.resolveMediaUrl(profile?.avatarUrl) : profile?.avatarUrl;
  return (
    <>
      <Avatar uid={c.id} name={c.isGroup ? c.title : profile?.name} url={url} size={36} />
      <span className="imkit-info-name"><ConversationTitle c={c} /></span>
    </>
  );
}

interface MessageListProps {
  conversation: ConversationId;
  summary: ConversationSummary | undefined;
  selection: ReadonlySet<number> | null;
  onToggle(seq: number): void;
  onReply(ref: ReplyRef): void;
  onForward(seq: number): void;
  onSelect(seq: number): void;
}

function MessageList({ conversation, summary, selection, onToggle, onReply, onForward, onSelect }: MessageListProps) {
  const { engine, strings, host, types } = useImKit();
  const messages = useMessages(conversation);
  const isGroup = isGroupConversation(conversation);
  const box = useRef<HTMLDivElement | null>(null);
  const atBottom = useRef(true);
  const loadingOlder = useRef(false);
  const [viewer, setViewer] = useState<string | null>(null);
  const [menu, setMenu] = useState<{ message: ChatMessage; x: number; y: number } | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const cursors = useSignal(useMemo(() => engine.readCursors(conversation), [engine, conversation]));
  // The group's members, for who has read each own message.
  const [members, setMembers] = useState<string[]>([]);
  useEffect(() => {
    if (!isGroup) return;
    let live = true;
    const load = (refresh: boolean) => void engine.members(conversation, refresh).then((list) => live && list && setMembers(list.map((m) => m.uid)));
    load(false);
    const off = engine.events.on((e) => {
      if (e.kind === "groupChanged" && e.conversation === conversation) load(true);
    });
    return () => {
      live = false;
      off();
    };
  }, [engine, conversation, isGroup]);

  // The "new messages" line goes above the first unread message as it was when the chat opened,
  // and only when something was unread then: what arrives while the user looks is not "new".
  const [opened] = useState(() => ({ lastReadSeq: summary?.lastReadSeq ?? 0, hadUnread: (summary?.unread ?? 0) > 0 }));
  const firstUnread = opened.hadUnread ? (messages.find((m) => m.seq !== null && m.seq > opened.lastReadSeq && m.from !== engine.me && !isRecalled(m))?.seq ?? null) : null;

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
      if (action === "copy") {
        await navigator.clipboard?.writeText(m.content);
        setToast(strings.copied);
      } else if (action === "save") saveMedia(scope.resolveUrl(m.fileUrl), m);
      else if (action === "reply" && m.seq !== null) onReply({ seq: m.seq, from: m.from, text: types.preview(m, strings) });
      else if (action === "forward" && m.seq !== null) onForward(m.seq);
      else if (action === "select" && m.seq !== null) onSelect(m.seq);
      else if (action === "recall") await engine.recall(m).catch(() => setToast(strings.recallFailed));
      else if (action === "retry") await engine.retry(m);
      else if (action === "discard") await engine.discard(m);
    } catch {
      setToast(strings.actionFailed);
    }
  };

  // Own messages show their state: in a single chat "Read" on the newest one the peer has read, in a group who has read each.
  const lastOwnSeq = [...messages].reverse().find((m) => m.from === engine.me && m.seq !== null && !isRecalled(m))?.seq ?? null;

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
      else if (isGroup && m.seq !== null) {
        const readers = readersOf(m, cursors, members);
        if (readers.length > 0) status = <ReadReceipts readers={readers} groupSize={members.length} />;
      } else if (m.seq === lastOwnSeq && (summary?.peerReadSeq ?? 0) >= m.seq!) status = <span className="imkit-status">{strings.read}</span>;
    }
    rows.push(
      <MessageRow
        key={m.seq !== null ? "s" + m.seq : "l" + m.localId}
        message={m}
        mine={m.from === engine.me}
        showSender={isGroup}
        scope={scope}
        status={status}
        selecting={selection !== null}
        selected={m.seq !== null && !!selection?.has(m.seq)}
        onToggle={() => m.seq !== null && onToggle(m.seq)}
        onMenu={(x, y) => selection === null && setMenu({ message: m, x, y })}
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

interface MessageRowProps {
  message: ChatMessage;
  mine: boolean;
  showSender: boolean;
  scope: MessageScope;
  status: ReactNode;
  /** Picking several messages: a click toggles the row instead of reaching what is in it. */
  selecting?: boolean;
  selected?: boolean;
  onToggle?(): void;
  onMenu(x: number, y: number): void;
}

function MessageRow({ message, mine, showSender, scope, status, selecting = false, selected = false, onToggle, onMenu }: MessageRowProps) {
  const { types, host } = useImKit();
  const sender = useProfile(showSender && !mine ? message.from : undefined);
  const press = useLongPress(onMenu);
  const type = types.typeOf(message);
  const content = type.render(message, { ...scope, isMine: mine });
  const quote = replyRefOf(message);
  // System notes and recall tombstones span the row: no avatar, bubble or status.
  if (type.name === "system" || isRecalled(message)) return <div className="imkit-system-row" data-seq={message.seq ?? undefined}>{content}</div>;
  const selectable = selecting && message.seq !== null;
  return (
    <div
      className={"imkit-row" + (mine ? " imkit-row-mine" : "") + (selectable ? " imkit-row-selectable" : "") + (selected ? " imkit-row-selected" : "")}
      data-seq={message.seq ?? undefined}
      data-type={type.name}
      onClickCapture={
        selecting
          ? (e) => {
              e.preventDefault();
              e.stopPropagation();
              if (selectable) onToggle?.();
            }
          : undefined
      }
    >
      {selectable && <input type="checkbox" className="imkit-row-check" checked={selected} readOnly aria-label={scope.strings.actionSelect} />}
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
          {quote && type.framed !== false ? <ReplyQuote quote={quote} /> : null}
          {content}
        </div>
        {status}
      </div>
    </div>
  );
}

/**
 * Who of `members` has read `message`, from the group's read positions; the
 * sender never counts, and a message still sending has no readers.
 */
export function readersOf(message: ChatMessage, cursors: ReadonlyMap<string, number>, members: readonly string[]): string[] {
  const seq = message.seq;
  if (seq === null || cursors.size === 0) return [];
  return members.filter((uid) => uid !== message.from && (cursors.get(uid) ?? 0) >= seq);
}

/** Up to three small avatars and "Read by N"; a click opens the full list. */
function ReadReceipts({ readers, groupSize }: { readers: string[]; groupSize: number }) {
  const { strings } = useImKit();
  const [open, setOpen] = useState(false);
  const shown = readers.slice(0, 3);
  return (
    <>
      <button type="button" className="imkit-status imkit-readers" onClick={() => setOpen(true)}>
        <span className="imkit-readers-faces" aria-hidden="true">
          {shown.map((uid) => <Reader key={uid} uid={uid} size={16} />)}
          {readers.length > shown.length && <span className="imkit-readers-more">+{readers.length - shown.length}</span>}
        </span>
        {format(strings.readByCount, readers.length)}
      </button>
      {open && <ReadByList readers={readers} totalEligible={Math.max(0, groupSize - 1)} onClose={() => setOpen(false)} />}
    </>
  );
}

function Reader({ uid, size }: { uid: string; size: number }) {
  const { host } = useImKit();
  const p = useProfile(uid);
  return <Avatar uid={uid} name={p?.name} url={host.resolveMediaUrl ? host.resolveMediaUrl(p?.avatarUrl) : p?.avatarUrl} size={size} />;
}

function ReaderRow({ uid }: { uid: string }) {
  const p = useProfile(uid);
  return (
    <div className="imkit-info-row" role="listitem">
      <Reader uid={uid} size={36} />
      <span className="imkit-info-name">{p?.name ?? shortUid(uid)}</span>
    </div>
  );
}

/** The full list. Rows open nothing: group members cannot open each other's profiles. */
function ReadByList({ readers, totalEligible, onClose }: { readers: string[]; totalEligible: number; onClose(): void }) {
  const { strings } = useImKit();
  const unread = Math.max(0, totalEligible - readers.length);
  const title = format(strings.readByCount, readers.length);
  return (
    <Dialog label={title} onClose={onClose}>
      <h3 className="imkit-dialog-title">{title}</h3>
      <div className="imkit-pick-list" role="list">
        {readers.map((uid) => <ReaderRow key={uid} uid={uid} />)}
      </div>
      {totalEligible > 0 && (
        <p className="imkit-info-hint" style={{ padding: "8px 0 0" }}>
          {unread > 0 ? format(strings.readByFooterPartial, totalEligible, unread) : format(strings.readByFooterAll, totalEligible)}
        </p>
      )}
      <div className="imkit-dialog-actions">
        <button type="button" className="imkit-text-button" onClick={onClose}>{strings.ok}</button>
      </div>
    </Dialog>
  );
}

function ReplyQuote({ quote }: { quote: ReplyRef }) {
  const { engine, strings } = useImKit();
  const who = useProfile(quote.from && quote.from !== engine.me ? quote.from : undefined);
  const name = quote.from === engine.me ? strings.you : (who?.name ?? "");
  return (
    <span className="imkit-quote">
      <span className="imkit-quote-name">{name}</span>
      <span className="imkit-quote-text">{quote.text}</span>
    </span>
  );
}

/** Downloads a media message (the browser may open cross-origin files instead). */
function saveMedia(url: string | undefined, m: ChatMessage): void {
  if (!url) return;
  const a = document.createElement("a");
  a.href = url;
  a.download = m.fileName || "imkit-" + (m.seq ?? "file");
  a.rel = "noreferrer";
  a.target = "_blank";
  document.body.appendChild(a);
  a.click();
  a.remove();
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
  const ownOnly: MessageAction[] = ["recall", "retry", "discard"];
  const actions = (types.typeOf(message).actions?.(message) ?? [])
    .filter((a) => mine || !ownOnly.includes(a))
    .filter((a, i, all) => all.indexOf(a) === i)
    .sort((a, b) => MESSAGE_ACTION_ORDER.indexOf(a) - MESSAGE_ACTION_ORDER.indexOf(b));
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
  const label: Record<MessageAction, string> = {
    copy: strings.actionCopy,
    save: strings.actionSave,
    reply: strings.actionReply,
    forward: strings.actionForward,
    select: strings.actionSelect,
    recall: strings.actionRecall,
    retry: strings.actionRetry,
    discard: strings.actionDiscard,
  };
  return (
    <div className="imkit-menu" style={{ left: Math.min(x, window.innerWidth - 160), top: Math.max(0, Math.min(y, window.innerHeight - 40 * actions.length - 16)) }} role="menu">
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

function Composer({ conversation, replyTo, onCancelReply }: { conversation: ConversationId; replyTo: ReplyRef | null; onCancelReply(): void }) {
  const { engine, strings, host } = useImKit();
  const replyWho = useProfile(replyTo && replyTo.from !== engine.me ? replyTo.from : undefined);
  const replyName = replyTo?.from === engine.me ? strings.you : (replyWho?.name ?? "");
  const summary = useConversation(conversation);
  const [text, setText] = useState(summary?.draft ?? "");
  const [mode, setMode] = useState<"text" | "voice">("text");
  const [emoji, setEmoji] = useState(false);
  const input = useRef<HTMLTextAreaElement | null>(null);
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
    const extra = replyTo ? replyExtra(replyTo) : undefined;
    onCancelReply();
    void engine.send(conversation, extra ? { content: body, extra } : { content: body });
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
    <div className="imkit-composer-wrap">
      {replyTo && (
        <div className="imkit-reply-bar">
          <div style={{ flex: 1, minWidth: 0 }}>
            <div className="imkit-reply-name">{format(strings.replyingTo, replyName)}</div>
            <div className="imkit-reply-text">{replyTo.text}</div>
          </div>
          <button type="button" className="imkit-icon-button" onClick={onCancelReply} aria-label={strings.cancel}>
            ✕
          </button>
        </div>
      )}
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
          ref={input}
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
      {mode === "text" && (
        <button type="button" className="imkit-icon-button" onClick={() => setEmoji(!emoji)} aria-label={strings.emoji} aria-pressed={emoji}>
          😊
        </button>
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
      {emoji && mode === "text" && (
        <div className="imkit-emoji-panel" role="listbox" aria-label={strings.emoji}>
          {EMOJI.map((e) => (
            <button
              key={e}
              type="button"
              role="option"
              aria-selected={false}
              onClick={() => {
                // In at the caret, then the caret after it.
                const el = input.current;
                const start = el?.selectionStart ?? text.length;
                const end = el?.selectionEnd ?? text.length;
                onInput(text.slice(0, start) + e + text.slice(end));
                requestAnimationFrame(() => {
                  el?.focus();
                  el?.setSelectionRange(start + e.length, start + e.length);
                });
              }}
            >
              {e}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/** The emoji panel's choice, the same as the Android kit's. */
export const EMOJI: readonly string[] = [
  "😀", "😃", "😄", "😁", "😅", "😂", "🤣", "😊", "😇", "🙂", "😉", "😌", "😍", "🥰", "😘", "😗",
  "😋", "😛", "😜", "🤪", "😝", "🤑", "🤗", "🤭", "🤫", "🤔", "🤐", "🤨", "😐", "😑", "😶", "😏",
  "😒", "🙄", "😬", "🤥", "😔", "😪", "🤤", "😴", "😷", "🤒", "🤕", "🤢", "🤮", "🤧", "🥵", "🥶",
  "😵", "🤯", "🤠", "🥳", "🥸", "😎", "🤓", "🧐", "😕", "😟", "🙁", "😮", "😯", "😲", "😳", "🥺",
  "😦", "😧", "😨", "😰", "😥", "😢", "😭", "😱", "😖", "😣", "😞", "😓", "😩", "😫", "🥱", "😤",
  "😡", "😠", "🤬", "😈", "👿", "💀", "☠️", "💩", "🤡", "👹", "👺", "👻", "👽", "👾", "🤖", "🎃",
  "👍", "👎", "👏", "🙌", "🤝", "💪", "🤞", "✌️", "🤟", "👌", "🤌", "🤏", "👆", "👇", "👉", "👈",
  "❤️", "🧡", "💛", "💚", "💙", "💜", "🖤", "🤍", "🤎", "💔", "❣️", "💕", "💞", "💓", "💗", "💖",
  "🎉", "🎊", "🎂", "🔥", "⭐", "🌟", "✨", "💯", "✅", "❌", "❓", "❗", "💤", "💢", "💦", "💨",
];

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

