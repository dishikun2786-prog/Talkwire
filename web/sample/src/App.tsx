import { NoMediaEngine, type CallTraceEntry } from "@talkwire/call";
import { CallButtons, CallOverlay, CallProvider, CallStyles, useCall } from "@talkwire/call/react";
import { BrowserNotifier, canManage, isGroupConversation, type ConversationId } from "@talkwire/client";
import { GroupJoinProvider, GroupJoinStyles, GroupQrCard, JoinApprovalSetting, JoinGroupScreen, JoinRequestsPanel, groupJoinNotice, useGroupJoin, useGroupJoinEvents } from "@talkwire/group-join/react";
import { ChatScreen, ConnectionBanner, ContactList, ConversationInfo, ConversationList, TalkwireProvider, TalkwireStyles, PickPeople, useSignal } from "@talkwire/react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { AdminConsole, isAdminHash } from "./Admin.js";
import { buildStack, createSpace, devLogin, sandboxInfo, saveSession, savedSession, type SandboxInfo, type Stack } from "./stack.js";

type Locale = "en" | "zh" | "vi";

const ui = {
  en: { title: "Talkwire sample", name: "Your name", signIn: "Sign in", signOut: "Sign out", contacts: "Contacts", newGroup: "New group", pick: "Pick a chat", noContacts: "Sign in as someone else in another browser to get a contact.", close: "Close", admin: "Admin",
    space: "Space code", newSpace: "New space", sandbox: "Public test service: anyone may use it, so do not enter anything private.", cleared: "Everything is cleared every day at", end: ".", docs: "Guide", invite: "Invite link", copied: "Copied", spaceHint: "Others who sign in to the same space become your contacts; spaces do not see each other.", noSpaceContacts: "Send the invite link to someone, or open it in another browser and sign in with another name." },
  zh: { title: "Talkwire 示例", name: "你的名字", signIn: "登录", signOut: "退出", contacts: "通讯录", newGroup: "发起群聊", pick: "选择一个聊天", noContacts: "在另一个浏览器用别的名字登录，就会多一个联系人。", close: "关闭", admin: "管理后台",
    space: "空间码", newSpace: "新建空间", sandbox: "公共测试服务：任何人都能用，请勿输入任何隐私信息。", cleared: "所有数据每天清空，时间", end: "。", docs: "使用说明", invite: "邀请链接", copied: "已复制", spaceHint: "登录同一个空间的人会互为联系人；不同空间互相看不见。", noSpaceContacts: "把邀请链接发给别人，或在另一个浏览器打开它、换个名字登录。" },
  vi: { title: "Mẫu Talkwire", name: "Tên của bạn", signIn: "Đăng nhập", signOut: "Đăng xuất", contacts: "Danh bạ", newGroup: "Tạo nhóm", pick: "Chọn một cuộc trò chuyện", noContacts: "Đăng nhập bằng tên khác ở trình duyệt khác để có danh bạ.", close: "Đóng", admin: "Quản trị",
    space: "Mã không gian", newSpace: "Tạo không gian", sandbox: "Dịch vụ thử nghiệm công khai: ai cũng dùng được, đừng nhập thông tin riêng tư.", cleared: "Mọi dữ liệu bị xóa mỗi ngày lúc", end: ".", docs: "Hướng dẫn", invite: "Liên kết mời", copied: "Đã chép", spaceHint: "Những người đăng nhập cùng không gian sẽ là danh bạ của nhau; các không gian không thấy nhau.", noSpaceContacts: "Gửi liên kết mời cho người khác, hoặc mở nó ở trình duyệt khác và đăng nhập bằng tên khác." },
} as const;

const SPACE_KEY = "talkwire.sample.space";

/** `?space=` from an invite link, else the space last used in this browser. */
function initialSpace(): string {
  const fromLink = new URLSearchParams(location.search).get("space");
  if (fromLink) return fromLink.trim().toLowerCase();
  try {
    return localStorage.getItem(SPACE_KEY) ?? "";
  } catch {
    return "";
  }
}

function rememberSpace(space: string): void {
  try {
    localStorage.setItem(SPACE_KEY, space);
  } catch {
    // private mode
  }
}

const inviteLink = (space: string) => `${location.origin}${location.pathname}?space=${encodeURIComponent(space)}`;

function initialLocale(): Locale {
  const l = navigator.language.toLowerCase();
  return l.startsWith("zh") ? "zh" : l.startsWith("vi") ? "vi" : "en";
}

/** In development every call trace line goes to the console (debug level), to find out why a call did not ring. */
const callLog = import.meta.env.DEV ? (e: CallTraceEntry) => console.debug("[talkwire-call]", new Date(e.at).toISOString(), e.what, e.callSessionId ?? "", e.note ?? "") : undefined;

/** In development, `talkwireCallTrace()` in the console prints the last 100 trace lines. */
function CallTraceOnWindow() {
  const { manager } = useCall();
  useEffect(() => {
    if (!import.meta.env.DEV) return;
    const w = window as unknown as { talkwireCallTrace?: () => void };
    w.talkwireCallTrace = () => console.table(manager.trace().map((e) => ({ ...e, at: new Date(e.at).toISOString() })));
    return () => {
      delete w.talkwireCallTrace;
    };
  }, [manager]);
  return null;
}

export function App() {
  const [locale, setLocale] = useState<Locale>(initialLocale);
  const [stack, setStack] = useState<Stack | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // `#/admin/...` is the admin console, beside the chat rather than inside it.
  const [admin, setAdmin] = useState(isAdminHash);
  // The public test service (sandbox) says so at /sandbox/info; the plain sample
  // host does not (null). undefined until asked.
  const [sandbox, setSandbox] = useState<SandboxInfo | null | undefined>(undefined);
  const [space, setSpace] = useState(initialSpace);
  const t = ui[locale];
  // Signaling only: the sample host's demo credentials lead to no RTC service (see examples/sample-host/src/demo-calls.ts).
  const media = useMemo(() => new NoMediaEngine(), []);

  const start = async (info: SandboxInfo | null, name?: string, spaceCode?: string) => {
    setBusy(true);
    setError(null);
    try {
      let session = name ? await devLogin(name, info ? spaceCode : undefined) : savedSession();
      if (!session) return;
      // In the sandbox a saved session is signed in again: the daily reset forgets
      // spaces and users, while the old token would still verify.
      if (!name && info) {
        if (!session.space) {
          saveSession(null);
          return;
        }
        session = await devLogin(session.name, session.space);
      }
      if (session.space) {
        rememberSpace(session.space);
        setSpace(session.space);
      }
      saveSession(session);
      setStack(await buildStack(session));
    } catch (e) {
      if (!name) saveSession(null);
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    void sandboxInfo().then((info) => {
      setSandbox(info);
      return start(info);
    });
  }, []);

  const newSpace = async () => {
    setBusy(true);
    setError(null);
    try {
      const code = await createSpace();
      rememberSpace(code);
      setSpace(code);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

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
    <div className="talkwire-root sample-root">
      <TalkwireStyles />
      <header className="sample-bar">
        <strong>{t.title}</strong>
        <span style={{ flex: 1 }} />
        <select aria-label="language" value={locale} onChange={(e) => setLocale(e.target.value as Locale)}>
          <option value="en">English</option>
          <option value="zh">中文</option>
          <option value="vi">Tiếng Việt</option>
        </select>
        {!admin && sandbox === null && <a href="#/admin/im/chats">{t.admin}</a>}
        {sandbox?.docsUrl && <a href={sandbox.docsUrl} target="_blank" rel="noreferrer">{t.docs}</a>}
        {stack && !admin && (
          <>
            {stack.session.space && <InviteLink space={stack.session.space} t={t} />}
            <LinkBadge stack={stack} />
            <span className="sample-me" data-testid="me">{stack.session.name}</span>
            <button type="button" onClick={signOut}>{t.signOut}</button>
          </>
        )}
      </header>
      {sandbox && (
        <p className="sample-sandbox" role="note">
          {t.sandbox}
          {sandbox.resetTime && <> {t.cleared} {sandbox.resetTime}{t.end}</>}
        </p>
      )}
      {admin && sandbox === null ? (
        <AdminConsole locale={locale} />
      ) : stack ? (
        <TalkwireProvider engine={stack.engine} host={stack.host} locale={locale}>
          <GroupJoinProvider>
            <CallProvider media={media} log={callLog}>
              <CallTraceOnWindow />
              <GroupJoinStyles />
              <CallStyles />
              <Chats stack={stack} t={t} sandbox={!!sandbox} />
              <CallOverlay />
            </CallProvider>
          </GroupJoinProvider>
        </TalkwireProvider>
      ) : sandbox === undefined ? null : (
        <form
          className="sample-login"
          onSubmit={(e) => {
            e.preventDefault();
            const name = new FormData(e.currentTarget).get("name");
            if (typeof name === "string") void start(sandbox, name.trim(), space.trim().toLowerCase());
          }}
        >
          <label>
            {t.name}
            <input name="name" required pattern="[A-Za-z0-9_]{2,20}" autoFocus autoComplete="username" />
          </label>
          {sandbox && (
            <>
              <label>
                {t.space}
                <span className="sample-space-row">
                  <input name="space" required pattern="[A-Za-z0-9]{8}" value={space} onChange={(e) => setSpace(e.target.value)} autoComplete="off" spellCheck={false} />
                  <button type="button" onClick={() => void newSpace()} disabled={busy}>{t.newSpace}</button>
                </span>
              </label>
              <p className="sample-hint">{t.spaceHint}</p>
            </>
          )}
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

function Chats({ stack, t, sandbox }: { stack: Stack; t: (typeof ui)[Locale]; sandbox: boolean }) {
  const [open, setOpen] = useState<ConversationId | null>(() => route().chat);
  const [joining, setJoining] = useState<string | null>(() => route().join);
  // The open chat's settings page, in place of the chat.
  const [info, setInfo] = useState<ConversationId | null>(null);
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
    setInfo(null);
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
          {contacts.length === 0 ? <p className="sample-hint">{sandbox ? t.noSpaceContacts : t.noContacts}</p> : <ContactList onOpen={openChat} />}
        </details>
      </aside>
      <main className="sample-chat">
        {joining ? (
          <div className="sample-join">
            <JoinGroupScreen qrToken={joining} onOpen={openChat} />
            <button type="button" onClick={() => setJoining(null)}>{t.close}</button>
          </div>
        ) : open && info === open ? (
          <ConversationInfo
            key={"info" + open}
            conversation={open}
            onBack={() => setInfo(null)}
            onLeft={() => openChat(null)}
            groupExtras={(topic, role) => (
              <div className="sample-group-extras">
                <GroupQrCard topic={topic} canRefresh={canManage(role)} linkFor={joinLink} />
                <JoinApprovalSetting topic={topic} editable={role === "owner"} />
                {canManage(role) && <JoinRequestsPanel topic={topic} />}
              </div>
            )}
          />
        ) : open ? (
          <ChatScreen
            key={open}
            conversation={open}
            onBack={() => setOpen(null)}
            onOpenInfo={() => setInfo(open)}
            headerActions={isGroupConversation(open) ? null : <CallButtons peer={open} onRefused={(text) => setBanner({ title: "", body: text, conversation: open })} />}
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
      {making && (
        <div className="sample-dialog" role="dialog" aria-label={t.newGroup}>
          <div className="sample-panel">
            <PickPeople onDone={(c) => { setMaking(false); openChat(c); }} onCancel={() => setMaking(false)} />
          </div>
        </div>
      )}
    </div>
  );
}

/** The space code and a button that copies a link signing others in to it. */
function InviteLink({ space, t }: { space: string; t: (typeof ui)[Locale] }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard?.writeText(inviteLink(space)).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };
  return (
    <span className="sample-space" data-testid="space" title={inviteLink(space)}>
      {t.space} <code>{space}</code>
      <button type="button" onClick={copy}>{copied ? t.copied : t.invite}</button>
    </span>
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
