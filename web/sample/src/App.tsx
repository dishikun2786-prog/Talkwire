import { NoMediaEngine } from "@imkit/call";
import { CallButtons, CallOverlay, CallProvider, CallStyles } from "@imkit/call/react";
import { BrowserNotifier, isGroupConversation, type ConversationId, type GroupRole } from "@imkit/client";
import { GroupJoinProvider, GroupJoinStyles, GroupQrCard, JoinApprovalSetting, JoinGroupScreen, JoinRequestsPanel, groupJoinNotice, useGroupJoin, useGroupJoinEvents } from "@imkit/group-join/react";
import { Avatar, ChatScreen, ConnectionBanner, ConversationList, ImKitProvider, ImKitStyles, useImKit, useSignal, type ImProfile } from "@imkit/react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { AdminConsole, isAdminHash } from "./Admin.js";
import { buildStack, devLogin, saveSession, savedSession, type Stack } from "./stack.js";

type Locale = "en" | "zh" | "vi";

const ui = {
  en: { title: "ImKit sample", name: "Your name", signIn: "Sign in", signOut: "Sign out", contacts: "Contacts", newGroup: "New group", groupName: "Group name", create: "Create", cancel: "Cancel", pick: "Pick a chat", noContacts: "Sign in as someone else in another browser to get a contact.", groupQr: "Group QR", close: "Close", admin: "Admin" },
  zh: { title: "ImKit 示例", name: "你的名字", signIn: "登录", signOut: "退出", contacts: "通讯录", newGroup: "发起群聊", groupName: "群名称", create: "创建", cancel: "取消", pick: "选择一个聊天", noContacts: "在另一个浏览器用别的名字登录，就会多一个联系人。", groupQr: "群二维码", close: "关闭", admin: "管理后台" },
  vi: { title: "Mẫu ImKit", name: "Tên của bạn", signIn: "Đăng nhập", signOut: "Đăng xuất", contacts: "Danh bạ", newGroup: "Tạo nhóm", groupName: "Tên nhóm", create: "Tạo", cancel: "Hủy", pick: "Chọn một cuộc trò chuyện", noContacts: "Đăng nhập bằng tên khác ở trình duyệt khác để có danh bạ.", groupQr: "Mã QR nhóm", close: "Đóng", admin: "Quản trị" },
} as const;

function initialLocale(): Locale {
  const l = navigator.language.toLowerCase();
  return l.startsWith("zh") ? "zh" : l.startsWith("vi") ? "vi" : "en";
}

export function App() {
  const [locale, setLocale] = useState<Locale>(initialLocale);
  const [stack, setStack] = useState<Stack | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // `#/admin/...` is the admin console, beside the chat rather than inside it.
  const [admin, setAdmin] = useState(isAdminHash);
  const t = ui[locale];
  // Signaling only: the sample host's demo credentials lead to no RTC service (see examples/sample-host/src/demo-calls.ts).
  const media = useMemo(() => new NoMediaEngine(), []);

  const start = async (name?: string) => {
    setBusy(true);
    setError(null);
    try {
      const session = name ? await devLogin(name) : savedSession();
      if (!session) return;
      saveSession(session);
      setStack(await buildStack(session));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    void start();
  }, []);

  useEffect(() => {
    const onHash = () => setAdmin(isAdminHash());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  const signOut = () => {
    stack?.close();
    saveSession(null);
    setStack(null);
  };

  return (
    <div className="imkit-root sample-root">
      <ImKitStyles />
      <header className="sample-bar">
        <strong>{t.title}</strong>
        <span style={{ flex: 1 }} />
        <select aria-label="language" value={locale} onChange={(e) => setLocale(e.target.value as Locale)}>
          <option value="en">English</option>
          <option value="zh">中文</option>
          <option value="vi">Tiếng Việt</option>
        </select>
        {!admin && <a href="#/admin/im/chats">{t.admin}</a>}
        {stack && !admin && (
          <>
            <LinkBadge stack={stack} />
            <span className="sample-me" data-testid="me">{stack.session.name}</span>
            <button type="button" onClick={signOut}>{t.signOut}</button>
          </>
        )}
      </header>
      {admin ? (
        <AdminConsole locale={locale} />
      ) : stack ? (
        <ImKitProvider engine={stack.engine} host={stack.host} locale={locale}>
          <GroupJoinProvider>
            <CallProvider media={media}>
              <GroupJoinStyles />
              <CallStyles />
              <Chats stack={stack} t={t} />
              <CallOverlay />
            </CallProvider>
          </GroupJoinProvider>
        </ImKitProvider>
      ) : (
        <form
          className="sample-login"
          onSubmit={(e) => {
            e.preventDefault();
            const name = new FormData(e.currentTarget).get("name");
            if (typeof name === "string") void start(name.trim());
          }}
        >
          <label>
            {t.name}
            <input name="name" required pattern="[A-Za-z0-9_]{2,20}" autoFocus autoComplete="username" />
          </label>
          <button type="submit" disabled={busy}>{t.signIn}</button>
          {error && <p className="sample-error" role="alert">{error}</p>}
        </form>
      )}
    </div>
  );
}

/** `#/chat/<conversation>` or `#/join/<qr token>`; a join link is this page with the second. */
function route(): { chat: ConversationId | null; join: string | null } {
  const m = /^#\/(chat|join)\/(.+)$/.exec(location.hash);
  const value = m ? decodeURIComponent(m[2]!) : null;
  return { chat: m?.[1] === "chat" ? value : null, join: m?.[1] === "join" ? value : null };
}

const joinLink = (qrToken: string) => `${location.origin}${location.pathname}#/join/${encodeURIComponent(qrToken)}`;

function Chats({ stack, t }: { stack: Stack; t: (typeof ui)[Locale] }) {
  const [open, setOpen] = useState<ConversationId | null>(() => route().chat);
  const [joining, setJoining] = useState<string | null>(() => route().join);
  const [panel, setPanel] = useState<ConversationId | null>(null);
  const [banner, setBanner] = useState<{ title: string; body: string; conversation: string } | null>(null);
  const [making, setMaking] = useState(false);
  const contacts = useSignal(stack.host.contacts);
  const { strings: joinStrings } = useGroupJoin();

  useEffect(() => {
    location.hash = joining ? "#/join/" + encodeURIComponent(joining) : open ? "#/chat/" + encodeURIComponent(open) : "";
  }, [open, joining]);
  // A join link pasted into this tab.
  useEffect(() => {
    const onHash = () => {
      const r = route();
      if (r.join) {
        setJoining(r.join);
        setOpen(null);
      }
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  const openChat = (c: ConversationId | null) => {
    setJoining(null);
    setOpen(c);
  };

  // Join requests for groups I manage, and the answer to mine.
  const nameOf = useCallback((uid: string) => contacts.find((c) => c.uid === uid)?.name ?? uid.slice(4, 10), [contacts]);
  useGroupJoinEvents(
    useCallback((ev) => setBanner({ title: ev.name, body: groupJoinNotice(ev, joinStrings, nameOf), conversation: ev.topic }), [joinStrings, nameOf]),
  );

  // Banners while the page is visible, system notifications when it is not.
  const notifier = useMemo(() => {
    const beep = () => {
      try {
        const ctx = new AudioContext();
        const osc = ctx.createOscillator();
        const gain = ctx.createGain();
        osc.frequency.value = 880;
        gain.gain.setValueAtTime(0.08, ctx.currentTime);
        gain.gain.exponentialRampToValueAtTime(0.0001, ctx.currentTime + 0.25);
        osc.connect(gain).connect(ctx.destination);
        osc.start();
        osc.stop(ctx.currentTime + 0.25);
      } catch {
        // no audio
      }
    };
    return new BrowserNotifier(stack.engine, {
      openConversation: () => openRef.current,
      render: (m, c, groupName) => ({ title: groupName ?? c?.title ?? "", body: m.content }),
      onBanner: (m, r) => setBanner({ ...r, conversation: m.conversation }),
      onOpen: (c) => setOpen(c),
      playSound: beep,
    });
  }, [stack]);
  const openRef = useMemo(() => ({ current: open }), []);
  openRef.current = open;
  useEffect(() => {
    notifier.start();
    void BrowserNotifier.requestPermission();
    return () => notifier.stop();
  }, [notifier]);
  useEffect(() => {
    if (!banner) return;
    const timer = setTimeout(() => setBanner(null), 4000);
    return () => clearTimeout(timer);
  }, [banner]);

  return (
    <div className="sample-main">
      <aside className="sample-side">
        <ConnectionBanner />
        <div className="sample-side-actions">
          <button type="button" onClick={() => setMaking(true)} disabled={contacts.length === 0}>{t.newGroup}</button>
        </div>
        <ConversationList onOpen={openChat} activeConversation={open} />
        <details className="sample-contacts" open>
          <summary>{t.contacts} ({contacts.length})</summary>
          {contacts.length === 0 && <p className="sample-hint">{t.noContacts}</p>}
          {contacts.map((c) => (
            <button key={c.uid} type="button" className="imkit-list-item" onClick={() => openChat(c.uid)} data-contact={c.name}>
              <Avatar uid={c.uid} name={c.name} url={c.avatarUrl} size={32} />
              <span>{c.name}</span>
            </button>
          ))}
        </details>
      </aside>
      <main className="sample-chat">
        {joining ? (
          <div className="sample-join">
            <JoinGroupScreen qrToken={joining} onOpen={openChat} />
            <button type="button" onClick={() => setJoining(null)}>{t.close}</button>
          </div>
        ) : open ? (
          <ChatScreen
            key={open}
            conversation={open}
            onBack={() => setOpen(null)}
            headerActions={
              isGroupConversation(open) ? (
                <button type="button" className="imkit-icon-button" onClick={() => setPanel(open)}>
                  {t.groupQr}
                </button>
              ) : (
                <CallButtons peer={open} onRefused={(text) => setBanner({ title: "", body: text, conversation: open })} />
              )
            }
          />
        ) : (
          <div className="sample-empty">{t.pick}</div>
        )}
      </main>
      {banner && (
        <button type="button" className="sample-banner" onClick={() => openChat(banner.conversation)}>
          <strong>{banner.title}</strong> {banner.body}
        </button>
      )}
      {panel && <GroupPanel topic={panel} t={t} onClose={() => setPanel(null)} />}
      {making && <NewGroup contacts={contacts} t={t} onClose={() => setMaking(false)} onCreate={async (name, members) => openChat(await stack.engine.createGroup(name, members))} />}
    </div>
  );
}

/** A group's QR code, the approval switch and, for managers, the join requests. */
function GroupPanel({ topic, t, onClose }: { topic: ConversationId; t: (typeof ui)[Locale]; onClose(): void }) {
  const { engine } = useImKit();
  const [role, setRole] = useState<GroupRole | null>(null);
  useEffect(() => {
    let live = true;
    void engine.myRole(topic).then((r) => live && setRole(r));
    return () => {
      live = false;
    };
  }, [engine, topic]);
  const manager = role === "owner" || role === "admin";
  return (
    <div className="sample-dialog" role="dialog" aria-label={t.groupQr}>
      <div className="sample-panel">
        <GroupQrCard topic={topic} canRefresh={manager} linkFor={joinLink} />
        {role && <JoinApprovalSetting topic={topic} editable={role === "owner"} />}
        {manager && <JoinRequestsPanel topic={topic} />}
        <button type="button" onClick={onClose}>{t.close}</button>
      </div>
    </div>
  );
}

/** How this tab reaches the server: through the SharedWorker, as the elected tab, or through it. */
function LinkBadge({ stack }: { stack: Stack }) {
  const role = useSignal(stack.tabs.role);
  return (
    <span className="sample-link" data-testid="link" title="worker · leader · follower · solo">
      {role}
    </span>
  );
}

function NewGroup({ contacts, t, onClose, onCreate }: { contacts: ImProfile[]; t: (typeof ui)[Locale]; onClose(): void; onCreate(name: string, members: string[]): Promise<void> }) {
  const [picked, setPicked] = useState<string[]>([]);
  const [name, setName] = useState("");
  return (
    <div className="sample-dialog" role="dialog" aria-label={t.newGroup}>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          await onCreate(name.trim(), picked);
          onClose();
        }}
      >
        <h3>{t.newGroup}</h3>
        <input aria-label={t.groupName} placeholder={t.groupName} value={name} onChange={(e) => setName(e.target.value)} required maxLength={30} />
        {contacts.map((c) => (
          <label key={c.uid} className="sample-pick">
            <input type="checkbox" checked={picked.includes(c.uid)} onChange={(e) => setPicked(e.target.checked ? [...picked, c.uid] : picked.filter((u) => u !== c.uid))} />
            {c.name}
          </label>
        ))}
        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <button type="button" onClick={onClose}>{t.cancel}</button>
          <button type="submit" disabled={!name.trim() || picked.length === 0}>{t.create}</button>
        </div>
      </form>
    </div>
  );
}
