/**
 * React components for scan-to-join: a group's QR card, the page a scanned
 * or opened link leads to, the managers' request list and the approval
 * switch. They need an `<ImKitProvider>` above a `<GroupJoinProvider>`, and
 * use the kit's CSS variables.
 */

import { RequestRefused } from "@imkit/client";
import { useImKit } from "@imkit/react";
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { GroupJoinClient, JOIN_MESSAGE_MAX, afterJoined, type GroupJoinEvent, type JoinRequest, type QrPreview, type QrToken } from "./index.js";

// ------------------------------------------------------------------ strings

export interface GroupJoinStrings {
  qrTitle: string;
  qrExpires: string;
  qrRefresh: string;
  qrRefreshConfirm: string;
  copyLink: string;
  copied: string;
  loading: string;
  memberCount: string;
  invalidCode: string;
  alreadyMember: string;
  groupFull: string;
  needsApproval: string;
  joinMessage: string;
  join: string;
  askToJoin: string;
  openChat: string;
  pending: string;
  failed: string;
  notAllowed: string;
  requestsTitle: string;
  noRequests: string;
  approve: string;
  reject: string;
  handledElsewhere: string;
  requireApproval: string;
  requestNotice: string;
  approvedNotice: string;
  rejectedNotice: string;
}

export const groupJoinEn: GroupJoinStrings = {
  qrTitle: "Group QR code",
  qrExpires: "Valid until {0}",
  qrRefresh: "Reset QR code",
  qrRefreshConfirm: "Reset the QR code? The old code and its link stop working at once.",
  copyLink: "Copy link",
  copied: "Copied",
  loading: "Loading…",
  memberCount: "{0} members",
  invalidCode: "This QR code is invalid or has expired.",
  alreadyMember: "You are already in this group.",
  groupFull: "This group is full.",
  needsApproval: "A group admin approves new members.",
  joinMessage: "Message to the admins (optional)",
  join: "Join group",
  askToJoin: "Ask to join",
  openChat: "Open chat",
  pending: "Request sent. You will be told when an admin decides.",
  failed: "Something went wrong. Try again.",
  notAllowed: "Only the group owner or admins can do this.",
  requestsTitle: "Join requests",
  noRequests: "No pending requests",
  approve: "Approve",
  reject: "Decline",
  handledElsewhere: "Another admin already handled this request.",
  requireApproval: "Admins approve people who join by QR code",
  requestNotice: "{0} asked to join {1}",
  approvedNotice: "You joined {0}",
  rejectedNotice: "Your request to join {0} was declined",
};

export const groupJoinZh: GroupJoinStrings = {
  qrTitle: "群二维码",
  qrExpires: "{0} 前有效",
  qrRefresh: "重置二维码",
  qrRefreshConfirm: "重置二维码？旧二维码和链接会立即失效。",
  copyLink: "复制链接",
  copied: "已复制",
  loading: "加载中…",
  memberCount: "{0} 人",
  invalidCode: "二维码不存在或已过期",
  alreadyMember: "你已在群里",
  groupFull: "群成员数量已达上限",
  needsApproval: "需要群管理员同意才能加入",
  joinMessage: "给管理员的留言（可选）",
  join: "加入群聊",
  askToJoin: "申请加入",
  openChat: "进入群聊",
  pending: "申请已发送，管理员处理后会通知你",
  failed: "操作失败，请重试",
  notAllowed: "只有群主或管理员可以执行此操作",
  requestsTitle: "入群申请",
  noRequests: "暂无待处理的申请",
  approve: "同意",
  reject: "拒绝",
  handledElsewhere: "该申请已被其他管理员处理",
  requireApproval: "通过二维码入群需管理员同意",
  requestNotice: "{0} 申请加入「{1}」",
  approvedNotice: "你的入群申请已通过，欢迎加入「{0}」",
  rejectedNotice: "你申请加入「{0}」被拒绝",
};

export const groupJoinVi: GroupJoinStrings = {
  qrTitle: "Mã QR nhóm",
  qrExpires: "Có hiệu lực đến {0}",
  qrRefresh: "Đặt lại mã QR",
  qrRefreshConfirm: "Đặt lại mã QR? Mã cũ và liên kết cũ sẽ hết hiệu lực ngay.",
  copyLink: "Sao chép liên kết",
  copied: "Đã sao chép",
  loading: "Đang tải…",
  memberCount: "{0} thành viên",
  invalidCode: "Mã QR không hợp lệ hoặc đã hết hạn.",
  alreadyMember: "Bạn đã ở trong nhóm này.",
  groupFull: "Nhóm đã đủ thành viên.",
  needsApproval: "Quản trị viên nhóm sẽ duyệt thành viên mới.",
  joinMessage: "Lời nhắn cho quản trị viên (tùy chọn)",
  join: "Tham gia nhóm",
  askToJoin: "Xin tham gia",
  openChat: "Mở cuộc trò chuyện",
  pending: "Đã gửi yêu cầu. Bạn sẽ được báo khi quản trị viên quyết định.",
  failed: "Có lỗi xảy ra. Hãy thử lại.",
  notAllowed: "Chỉ chủ nhóm hoặc quản trị viên mới làm được việc này.",
  requestsTitle: "Yêu cầu tham gia",
  noRequests: "Không có yêu cầu đang chờ",
  approve: "Đồng ý",
  reject: "Từ chối",
  handledElsewhere: "Quản trị viên khác đã xử lý yêu cầu này.",
  requireApproval: "Người vào bằng mã QR cần quản trị viên duyệt",
  requestNotice: "{0} xin tham gia {1}",
  approvedNotice: "Bạn đã tham gia {0}",
  rejectedNotice: "Yêu cầu tham gia {0} của bạn đã bị từ chối",
};

/** The built-in table for a BCP 47 tag; English when the language is not built in. */
export function groupJoinStringsFor(locale: string): GroupJoinStrings {
  const lang = locale.toLowerCase().split("-")[0];
  return lang === "zh" ? groupJoinZh : lang === "vi" ? groupJoinVi : groupJoinEn;
}

const fill = (template: string, ...args: (string | number)[]) => template.replace(/\{(\d+)\}/g, (m, i: string) => (args[Number(i)] !== undefined ? String(args[Number(i)]) : m));

/** The notice for an event, for a banner or a toast. */
export function groupJoinNotice(ev: GroupJoinEvent, strings: GroupJoinStrings, nameOf: (uid: string) => string = (u) => u): string {
  if (ev.kind === "request") return fill(strings.requestNotice, nameOf(ev.from), ev.name);
  return fill(ev.status === "approved" ? strings.approvedNotice : strings.rejectedNotice, ev.name);
}

export const groupJoinCss = `
.imkit-gj { display: flex; flex-direction: column; gap: 12px; padding: 16px; background: var(--imkit-surface); border-radius: var(--imkit-radius); }
.imkit-gj h2 { margin: 0; font-size: 17px; }
.imkit-gj-muted { color: var(--imkit-text-muted); font-size: 13px; margin: 0; }
.imkit-gj-error { color: var(--imkit-danger); font-size: 13px; margin: 0; }
.imkit-gj-link { word-break: break-all; font-family: ui-monospace, monospace; font-size: 12px; padding: 8px; border: 1px solid var(--imkit-border); border-radius: 8px; }
.imkit-gj-row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.imkit-gj-request { display: flex; gap: 8px; align-items: center; padding: 10px 0; border-bottom: 1px solid var(--imkit-border); }
.imkit-gj-request > div { flex: 1; min-width: 0; }
.imkit-gj-request p { margin: 2px 0 0; color: var(--imkit-text-muted); font-size: 13px; overflow-wrap: anywhere; }
.imkit-gj-secondary { border: 1px solid var(--imkit-border); border-radius: 18px; padding: 7px 14px; background: transparent; color: inherit; font: inherit; cursor: pointer; }
.imkit-gj-secondary:disabled { opacity: .5; cursor: default; }
.imkit-gj textarea { resize: vertical; min-height: 64px; border: 1px solid var(--imkit-border); border-radius: 10px; padding: 8px 10px; font: inherit; background: var(--imkit-bg); color: inherit; }
`;

/** Puts the module's stylesheet in the page once (next to `<ImKitStyles>`). */
export function GroupJoinStyles() {
  return <style data-imkit-group-join="">{groupJoinCss}</style>;
}

// ------------------------------------------------------------------ context

interface GroupJoinValue {
  api: GroupJoinClient;
  strings: GroupJoinStrings;
}

const GroupJoinContext = createContext<GroupJoinValue | null>(null);

export interface GroupJoinProviderProps {
  /** Single texts to replace in the table chosen by the kit's locale. */
  strings?: Partial<GroupJoinStrings>;
  children: ReactNode;
}

/** Makes the module available below; one per `<ImKitProvider>`. */
export function GroupJoinProvider({ strings, children }: GroupJoinProviderProps) {
  const { engine, locale } = useImKit();
  const api = useMemo(() => new GroupJoinClient(engine.client), [engine]);
  // Start in the effect, not only in the constructor: StrictMode runs the
  // cleanup once before mounting again, and the listening must survive it.
  useEffect(() => {
    api.start();
    return () => api.dispose();
  }, [api]);
  const value = useMemo(() => ({ api, strings: { ...groupJoinStringsFor(locale), ...strings } }), [api, locale, strings]);
  return <GroupJoinContext.Provider value={value}>{children}</GroupJoinContext.Provider>;
}

export function useGroupJoin(): GroupJoinValue {
  const v = useContext(GroupJoinContext);
  if (!v) throw new Error("scan-to-join components need a <GroupJoinProvider>");
  return v;
}

/** Calls `listener` for join requests and review results while mounted. */
export function useGroupJoinEvents(listener: (ev: GroupJoinEvent) => void): void {
  const { api } = useGroupJoin();
  useEffect(() => api.events.on(listener), [api, listener]);
}

const codeOf = (e: unknown) => (e instanceof RequestRefused ? e.reply.code : 0);

// ------------------------------------------------------------------ QR card

export interface GroupQrCardProps {
  topic: string;
  /** Managers may reset the code. */
  canRefresh?: boolean;
  /** What the code encodes: usually a link to the app's join page. Default: the token itself. */
  linkFor?: (qrToken: string) => string;
  /** Draws the QR code; without it the link is shown as text. The kit ships no QR encoder. */
  renderQr?: (value: string) => ReactNode;
}

/** A group's current QR code and link, for members to share. */
export function GroupQrCard({ topic, canRefresh, linkFor = (t) => t, renderQr }: GroupQrCardProps) {
  const { api, strings } = useGroupJoin();
  const { locale } = useImKit();
  const [token, setToken] = useState<QrToken | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);

  const load = useCallback(
    async (refresh: boolean) => {
      setBusy(true);
      setError(null);
      try {
        setToken(await api.qrToken(topic, { refresh }));
      } catch (e) {
        setError(codeOf(e) === 401 ? strings.notAllowed : strings.failed);
      } finally {
        setBusy(false);
      }
    },
    [api, topic, strings],
  );
  useEffect(() => {
    void load(false);
  }, [load]);

  const link = token ? linkFor(token.qrToken) : "";
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
      setTimeout(() => setCopied(false), 1_500);
    } catch {
      // no clipboard access: the link is on screen to copy by hand
    }
  };

  return (
    <section className="imkit-gj" aria-label={strings.qrTitle}>
      <h2>{strings.qrTitle}</h2>
      {error && <p className="imkit-gj-error">{error}</p>}
      {!token && !error && <p className="imkit-gj-muted">{strings.loading}</p>}
      {token && (
        <>
          {renderQr ? renderQr(link) : null}
          <div className="imkit-gj-link" data-qr-link="">
            {link}
          </div>
          <p className="imkit-gj-muted">{fill(strings.qrExpires, new Date(token.expiresAt).toLocaleString(locale))}</p>
          <div className="imkit-gj-row">
            <button type="button" className="imkit-send" onClick={() => void copy()}>
              {copied ? strings.copied : strings.copyLink}
            </button>
            {canRefresh && (
              <button type="button" className="imkit-gj-secondary" disabled={busy} onClick={() => window.confirm(strings.qrRefreshConfirm) && void load(true)}>
                {strings.qrRefresh}
              </button>
            )}
          </div>
        </>
      )}
    </section>
  );
}

// ------------------------------------------------------------------ joining

export interface JoinGroupScreenProps {
  qrToken: string;
  /** Opens the group's chat; shown once the user is a member. */
  onOpen?: (topic: string) => void;
}

type JoinPhase =
  | { kind: "loading" }
  | { kind: "invalid" }
  | { kind: "preview"; preview: QrPreview; error?: string }
  | { kind: "joining"; preview: QrPreview }
  | { kind: "member"; preview: QrPreview }
  | { kind: "pending"; preview: QrPreview };

/** Where a scanned or opened link leads: the group, then joining or asking to join. */
export function JoinGroupScreen({ qrToken, onOpen }: JoinGroupScreenProps) {
  const { api, strings } = useGroupJoin();
  const { engine } = useImKit();
  const [phase, setPhase] = useState<JoinPhase>({ kind: "loading" });
  const [message, setMessage] = useState("");

  useEffect(() => {
    let live = true;
    setPhase({ kind: "loading" });
    api.resolve(qrToken).then(
      (preview) => live && setPhase(!preview ? { kind: "invalid" } : preview.alreadyMember ? { kind: "member", preview } : { kind: "preview", preview }),
      () => live && setPhase({ kind: "invalid" }),
    );
    return () => {
      live = false;
    };
  }, [api, qrToken]);

  const join = async (preview: QrPreview) => {
    setPhase({ kind: "joining", preview });
    try {
      const r = await api.join(qrToken, preview.requireApproval ? message.trim() || undefined : undefined);
      if (r.status === "pending_approval") {
        setPhase({ kind: "pending", preview });
        return;
      }
      await afterJoined(engine, r.topic || preview.topic, preview.name);
      setPhase({ kind: "member", preview });
    } catch (e) {
      const code = codeOf(e);
      if (code === 404) setPhase({ kind: "invalid" });
      else setPhase({ kind: "preview", preview: code === 409 ? { ...preview, isFull: true } : preview, error: code === 409 ? strings.groupFull : strings.failed });
    }
  };

  if (phase.kind === "loading") {
    return (
      <section className="imkit-gj">
        <p className="imkit-gj-muted">{strings.loading}</p>
      </section>
    );
  }
  if (phase.kind === "invalid") {
    return (
      <section className="imkit-gj">
        <p className="imkit-gj-error">{strings.invalidCode}</p>
      </section>
    );
  }
  const { preview } = phase;
  return (
    <section className="imkit-gj" aria-label={preview.name}>
      <h2>{preview.name}</h2>
      <p className="imkit-gj-muted">{fill(strings.memberCount, preview.memberCount)}</p>
      {phase.kind === "member" && (
        <>
          <p>{strings.alreadyMember}</p>
          {onOpen && (
            <button type="button" className="imkit-send" onClick={() => onOpen(preview.topic)}>
              {strings.openChat}
            </button>
          )}
        </>
      )}
      {phase.kind === "pending" && <p>{strings.pending}</p>}
      {(phase.kind === "preview" || phase.kind === "joining") && (
        <>
          {preview.isFull && <p className="imkit-gj-error">{strings.groupFull}</p>}
          {preview.requireApproval && !preview.isFull && (
            <>
              <p className="imkit-gj-muted">{strings.needsApproval}</p>
              <textarea
                aria-label={strings.joinMessage}
                placeholder={strings.joinMessage}
                value={message}
                onChange={(e) => setMessage(Array.from(e.target.value).slice(0, JOIN_MESSAGE_MAX).join(""))}
              />
            </>
          )}
          {phase.kind === "preview" && phase.error && !preview.isFull && <p className="imkit-gj-error">{phase.error}</p>}
          {!preview.isFull && (
            <button type="button" className="imkit-send" disabled={phase.kind === "joining"} onClick={() => void join(preview)}>
              {preview.requireApproval ? strings.askToJoin : strings.join}
            </button>
          )}
        </>
      )}
    </section>
  );
}

// ------------------------------------------------------------------ managing

export interface JoinRequestsPanelProps {
  topic: string;
  /** Names applicants; default: the name the server gave. */
  nameOf?: (request: JoinRequest) => string;
}

/** A group's pending join requests, with approve and decline. Managers only. */
export function JoinRequestsPanel({ topic, nameOf = (r) => r.requesterName }: JoinRequestsPanelProps) {
  const { api, strings } = useGroupJoin();
  const [requests, setRequests] = useState<JoinRequest[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  const load = useCallback(async () => {
    try {
      setRequests(await api.requests(topic));
      setError(null);
    } catch (e) {
      setError(codeOf(e) === 401 ? strings.notAllowed : strings.failed);
    }
  }, [api, topic, strings]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(
    () =>
      api.events.on((ev) => {
        if (ev.kind === "request" && ev.topic === topic) void load();
      }),
    [api, topic, load],
  );

  const review = async (r: JoinRequest, approve: boolean) => {
    setBusy(r.id);
    try {
      await api.review(topic, r.id, approve);
      setRequests((list) => list?.filter((x) => x.id !== r.id) ?? null);
      setError(null);
    } catch (e) {
      const code = codeOf(e);
      const errCode = e instanceof RequestRefused ? e.reply.errCode : undefined;
      if (code === 409 && errCode === "group_full") {
        // strict servers check first: the request stays pending.
        setError(strings.groupFull);
      } else if (code === 409) {
        // Handled by someone else; on a legacy server also a failed check
        // after the request was used up (D8). Either way it is no longer
        // pending: show the server's list, then say why it changed.
        await load();
        setError(strings.handledElsewhere);
      } else {
        setError(code === 401 ? strings.notAllowed : strings.failed);
      }
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="imkit-gj" aria-label={strings.requestsTitle}>
      <h2>{strings.requestsTitle}</h2>
      {error && <p className="imkit-gj-error">{error}</p>}
      {requests === null && !error && <p className="imkit-gj-muted">{strings.loading}</p>}
      {requests?.length === 0 && <p className="imkit-gj-muted">{strings.noRequests}</p>}
      {requests?.map((r) => (
        <div key={r.id} className="imkit-gj-request" data-join-request={r.id}>
          <div>
            <strong>{nameOf(r)}</strong>
            {r.message && <p>{r.message}</p>}
          </div>
          <button type="button" className="imkit-gj-secondary" disabled={busy === r.id} onClick={() => void review(r, false)}>
            {strings.reject}
          </button>
          <button type="button" className="imkit-send" disabled={busy === r.id} onClick={() => void review(r, true)}>
            {strings.approve}
          </button>
        </div>
      ))}
    </section>
  );
}

export interface JoinApprovalSettingProps {
  topic: string;
  /** Only the owner may change it; others see it read-only. */
  editable?: boolean;
}

/** The "admins approve people who join by QR code" switch. */
export function JoinApprovalSetting({ topic, editable }: JoinApprovalSettingProps) {
  const { api, strings } = useGroupJoin();
  const [on, setOn] = useState<boolean | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api.requireApproval(topic).then(
      (v) => live && setOn(v),
      () => live && setError(strings.failed),
    );
    return () => {
      live = false;
    };
  }, [api, topic, strings]);

  const toggle = async (next: boolean) => {
    setError(null);
    // The box follows the click at once; the server's answer settles it.
    setOn(next);
    try {
      setOn(await api.setRequireApproval(topic, next));
    } catch (e) {
      setOn(!next);
      setError(codeOf(e) === 401 ? strings.notAllowed : strings.failed);
    }
  };

  return (
    <div className="imkit-gj-row">
      <label className="imkit-gj-row">
        <input type="checkbox" checked={on ?? false} disabled={!editable || on === null} onChange={(e) => void toggle(e.target.checked)} />
        {strings.requireApproval}
      </label>
      {error && <p className="imkit-gj-error">{error}</p>}
    </div>
  );
}
