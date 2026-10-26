/**
 * Administrator pages for Talkwire, to embed in the host's own admin console:
 * chat records (with clearing a two-person conversation) and call records.
 * Put them under an `<ImAdminProvider>`. The role only decides what is
 * shown; the server checks every request again.
 */

import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import {
  clearConfirmation,
  ImAdminError,
  type AdminMessage,
  type AdminTopic,
  type CallRecord,
  type CallsQuery,
  type CallStatus,
  type CallType,
  type ClearResult,
  type ImAdminApi,
  type MessageType,
  type Person,
} from "./api.js";
import { adminStringsFor, type AdminStrings } from "./strings.js";

// ------------------------------------------------------------------ context

export type AdminRole = "admin" | "superadmin";

interface AdminValue {
  api: ImAdminApi;
  role: AdminRole;
  locale: string;
  strings: AdminStrings;
}

const AdminContext = createContext<AdminValue | null>(null);

export interface ImAdminProviderProps {
  api: ImAdminApi;
  /** The signed-in administrator's role: only a superadmin is offered clearing. */
  role: AdminRole;
  /** BCP 47; default en. */
  locale?: string;
  /** Texts for a language not built in. */
  strings?: AdminStrings;
  children: ReactNode;
}

export function ImAdminProvider({ api, role, locale = "en", strings, children }: ImAdminProviderProps) {
  const value = useMemo(() => ({ api, role, locale, strings: strings ?? adminStringsFor(locale) }), [api, role, locale, strings]);
  return (
    <AdminContext.Provider value={value}>
      <div className="talkwire-admin">{children}</div>
    </AdminContext.Provider>
  );
}

export function useImAdmin(): AdminValue {
  const value = useContext(AdminContext);
  if (!value) throw new Error("Talkwire admin pages need an <ImAdminProvider> above them");
  return value;
}

// ------------------------------------------------------------------ helpers

export function personName(p: Person, s: AdminStrings): string {
  return p.displayName || p.userId || `${s.someone} (${p.uid})`;
}

/** h:mm:ss or m:ss. */
export function formatDuration(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const ss = String(sec % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

const formatTime = (at: number | string, locale: string) => new Date(at).toLocaleString(locale);

/** A date input's value as the start or end of that day, local time. */
const dayStart = (day: string) => (day ? new Date(`${day}T00:00:00`).getTime() : undefined);
const dayEnd = (day: string) => (day ? new Date(`${day}T23:59:59.999`).getTime() : undefined);

const failure = (err: unknown, s: AdminStrings) => (err instanceof ImAdminError ? err.message : s.unreachable);

/**
 * Links in messages come from users: only http(s) and same-site paths open,
 * never javascript: or data:.
 */
export function safeLink(url: string | undefined): string | undefined {
  if (!url) return undefined;
  try {
    const u = new URL(url, globalThis.location?.href ?? "http://localhost/");
    return u.protocol === "https:" || u.protocol === "http:" ? url : undefined;
  } catch {
    return undefined;
  }
}

type Kind = MessageType | "system";

function kindOf(contentType: string): Kind {
  if (contentType === "text" || contentType === "system") return contentType;
  const head = contentType.split("/")[0];
  return head === "image" || head === "video" || head === "audio" ? head : "file";
}

/** The outcome of a call as one label: ringing, in a call, or how it ended. */
export function callStatusText(r: Pick<CallRecord, "status" | "endReason">, s: AdminStrings): string {
  if (r.status !== "ended") return s.statuses[r.status];
  return s.outcomes[r.endReason ?? ""] ?? s.endedOther(r.endReason ?? "");
}

function callTone(r: Pick<CallRecord, "status" | "endReason">): string {
  if (r.status !== "ended") return "live";
  if (r.endReason === "user_hangup") return "ok";
  return r.endReason === "failed" || r.endReason === "disconnected" ? "bad" : "muted";
}

function callTypeText(r: Pick<CallRecord, "requestedType" | "callType">, s: AdminStrings): string {
  return r.requestedType === r.callType ? s.callTypes[r.callType] : `${s.callTypes[r.requestedType]} → ${s.callTypes[r.callType]}`;
}

/** Runs async loads whose answers can come back out of order: only the latest is used. */
function useLatest() {
  const seq = useRef(0);
  return useCallback(() => {
    const n = ++seq.current;
    return () => n === seq.current;
  }, []);
}

/** A fresh idempotency key for one clear. */
function newKey(): string {
  const c = globalThis.crypto;
  if (c?.randomUUID) return c.randomUUID();
  const bytes = new Uint8Array(16);
  c.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

function PersonLine({ p }: { p: Person }) {
  const { strings: s } = useImAdmin();
  return (
    <span className="talkwire-admin-person">
      <span>{personName(p, s)}</span>
      {p.userId && p.displayName ? <span className="talkwire-admin-muted talkwire-admin-mono">{p.userId}</span> : null}
    </span>
  );
}

// ------------------------------------------------------------------ chat records

export interface ChatRecordsProps {
  /** Searched for when the page opens. */
  userId?: string;
}

/** A user's conversations, and the messages of the one picked. */
export function ChatRecords({ userId: initial = "" }: ChatRecordsProps) {
  const { api, strings: s, locale } = useImAdmin();
  const [input, setInput] = useState(initial);
  const [userId, setUserId] = useState(initial.trim());
  const [topics, setTopics] = useState<AdminTopic[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const latest = useLatest();
  const inputId = useId();

  const load = useCallback(
    async (uid: string, keepOpen: boolean) => {
      const current = latest();
      if (!keepOpen) {
        setTopics(null);
        setOpen(null);
      }
      setError(null);
      try {
        const r = await api.userTopics(uid, 200);
        if (current()) setTopics(r.topics);
      } catch (err) {
        if (current()) setError(failure(err, s));
      }
    },
    [api, s, latest],
  );

  useEffect(() => {
    if (userId) void load(userId, false);
  }, [userId, load]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const v = input.trim();
    if (!v) return;
    if (v === userId) void load(v, false);
    else setUserId(v);
  };

  const picked = topics?.find((t) => t.topic === open);
  const title = (t: AdminTopic) =>
    t.isGroup ? t.name || t.topic : (() => {
      const other = t.members.find((m) => m.userId !== userId) ?? t.members[0];
      return other ? personName(other, s) : t.topic;
    })();

  return (
    <section className="talkwire-admin-page" aria-label={s.chatRecords}>
      <form className="talkwire-admin-filters" onSubmit={submit} role="search">
        <label htmlFor={inputId}>{s.userId}</label>
        <input id={inputId} value={input} onChange={(e) => setInput(e.target.value)} placeholder={s.userIdHint} />
        <button type="submit">{s.search}</button>
      </form>
      {error ? (
        <p role="alert" className="talkwire-admin-error">
          {error}
        </p>
      ) : null}
      {userId && !error ? (
        <div className="talkwire-admin-split">
          <ul className="talkwire-admin-topics" aria-label={s.chatRecords}>
            {topics === null ? (
              <li className="talkwire-admin-muted">{s.loading}</li>
            ) : topics.length === 0 ? (
              <li className="talkwire-admin-muted">{s.noTopics}</li>
            ) : (
              topics.map((t) => (
                <li key={t.topic}>
                  <button type="button" aria-current={open === t.topic ? "true" : undefined} onClick={() => setOpen(t.topic)}>
                    <span className="talkwire-admin-topic-title">
                      {title(t)} <span className="talkwire-admin-tag">{t.isGroup ? s.group : s.direct}</span>
                    </span>
                    <span className="talkwire-admin-muted">
                      {s.messages(t.messageCount)}
                      {t.lastMessageAt ? ` · ${formatTime(t.lastMessageAt, locale)}` : ""}
                    </span>
                  </button>
                </li>
              ))
            )}
          </ul>
          <div className="talkwire-admin-detail">
            {picked ? (
              <TopicMessages key={picked.topic} topic={picked} targetUserId={userId} onCleared={() => void load(userId, true)} />
            ) : (
              <p className="talkwire-admin-muted">{s.pickTopic}</p>
            )}
          </div>
        </div>
      ) : null}
    </section>
  );
}

interface MessageFilter {
  type: "" | MessageType;
  keyword: string;
  from: string;
  to: string;
}

const PAGE = 50;

export interface TopicMessagesProps {
  topic: AdminTopic;
  /** The user whose records are being looked at, kept in the audit of a clear. */
  targetUserId?: string;
  onCleared?(result: ClearResult): void;
}

/** One conversation's messages, newest first, with filters and paging; clearing for a superadministrator. */
export function TopicMessages({ topic, targetUserId, onCleared }: TopicMessagesProps) {
  const { api, role, strings: s, locale } = useImAdmin();
  const [filter, setFilter] = useState<MessageFilter>({ type: "", keyword: "", from: "", to: "" });
  const [applied, setApplied] = useState(filter);
  const [rows, setRows] = useState<AdminMessage[] | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [reload, setReload] = useState(0);
  const latest = useLatest();
  const ids = { type: useId(), keyword: useId(), from: useId(), to: useId() };

  const query = (f: MessageFilter, beforeSeq?: number) => ({
    limit: PAGE,
    beforeSeq,
    type: f.type || undefined,
    keyword: f.keyword.trim() || undefined,
    fromMs: dayStart(f.from),
    toMs: dayEnd(f.to),
  });

  useEffect(() => {
    const current = latest();
    setRows(null);
    setError(null);
    api.messages(topic.topic, query(applied)).then(
      (r) => {
        if (!current()) return;
        setRows(r.messages);
        setHasMore(r.hasMore);
      },
      (err) => {
        if (current()) setError(failure(err, s));
      },
    );
  }, [api, topic.topic, applied, reload, latest, s]);

  const older = async () => {
    if (!rows?.length || busy) return;
    const current = latest();
    setBusy(true);
    try {
      const r = await api.messages(topic.topic, query(applied, Math.min(...rows.map((m) => m.seq))));
      if (!current()) return;
      setRows([...rows, ...r.messages]);
      setHasMore(r.hasMore);
    } catch (err) {
      if (current()) setError(failure(err, s));
    } finally {
      setBusy(false);
    }
  };

  const sorted = rows ? [...rows].sort((a, b) => b.seq - a.seq) : null;

  return (
    <section className="talkwire-admin-messages" aria-label={topic.topic}>
      <div className="talkwire-admin-bar">
        <span className="talkwire-admin-mono talkwire-admin-muted">{topic.topic}</span>
        {role === "superadmin" && !topic.isGroup ? (
          <button type="button" className="talkwire-admin-danger" onClick={() => setClearing(true)}>
            {s.clear}
          </button>
        ) : null}
      </div>
      <form
        className="talkwire-admin-filters"
        onSubmit={(e) => {
          e.preventDefault();
          setApplied({ ...filter }); // a new object: applying unchanged filters loads again
        }}
      >
        <label htmlFor={ids.type}>{s.type}</label>
        <select id={ids.type} value={filter.type} onChange={(e) => setFilter({ ...filter, type: e.target.value as MessageFilter["type"] })}>
          <option value="">{s.allTypes}</option>
          {(Object.keys(s.types) as MessageType[]).map((k) => (
            <option key={k} value={k}>
              {s.types[k]}
            </option>
          ))}
        </select>
        <label htmlFor={ids.keyword}>{s.keyword}</label>
        <input id={ids.keyword} value={filter.keyword} maxLength={100} onChange={(e) => setFilter({ ...filter, keyword: e.target.value })} />
        <label htmlFor={ids.from}>{s.from}</label>
        <input id={ids.from} type="date" value={filter.from} onChange={(e) => setFilter({ ...filter, from: e.target.value })} />
        <label htmlFor={ids.to}>{s.to}</label>
        <input id={ids.to} type="date" value={filter.to} onChange={(e) => setFilter({ ...filter, to: e.target.value })} />
        <button type="submit">{s.apply}</button>
      </form>
      {error ? (
        <p role="alert" className="talkwire-admin-error">
          {error}
        </p>
      ) : null}
      {sorted === null ? (
        error ? null : <p className="talkwire-admin-muted">{s.loading}</p>
      ) : sorted.length === 0 ? (
        <p className="talkwire-admin-muted">{s.noMessages}</p>
      ) : (
        <ol className="talkwire-admin-message-list">
          {sorted.map((m) => (
            <li key={m.seq} data-seq={m.seq}>
              <div className="talkwire-admin-message-head">
                <PersonLine p={m.sender} />
                <time className="talkwire-admin-muted" dateTime={new Date(m.ts).toISOString()}>
                  {formatTime(m.ts, locale)}
                </time>
              </div>
              <MessageBody m={m} />
            </li>
          ))}
        </ol>
      )}
      {hasMore && sorted?.length ? (
        <button type="button" className="talkwire-admin-more" disabled={busy} onClick={() => void older()}>
          {busy ? s.loading : s.loadOlder}
        </button>
      ) : null}
      {clearing ? (
        <ClearTopicDialog
          topic={topic.topic}
          targetUserId={targetUserId}
          onClose={() => setClearing(false)}
          onCleared={(r) => {
            setReload((n) => n + 1);
            onCleared?.(r);
          }}
        />
      ) : null}
    </section>
  );
}

function MessageBody({ m }: { m: AdminMessage }) {
  const { strings: s } = useImAdmin();
  const kind = kindOf(m.contentType);
  if (kind === "text") return <p className="talkwire-admin-text">{m.content}</p>;
  if (kind === "system") {
    return (
      <p className="talkwire-admin-text talkwire-admin-muted">
        <span className="talkwire-admin-tag">{s.system}</span> {m.content}
      </p>
    );
  }
  const href = safeLink(m.fileUrl);
  const label = m.fileName || m.fileUrl || m.content;
  return (
    <p className="talkwire-admin-text">
      <span className="talkwire-admin-tag">{s.types[kind]}</span>{" "}
      {href ? (
        <a href={href} target="_blank" rel="noopener noreferrer">
          {label}
        </a>
      ) : (
        <span>{label}</span>
      )}
      {m.duration ? <span className="talkwire-admin-muted"> · {formatDuration(Math.round(m.duration))}</span> : null}
    </p>
  );
}

export interface ClearTopicDialogProps {
  topic: string;
  targetUserId?: string;
  onClose(): void;
  onCleared?(result: ClearResult): void;
}

/**
 * Clears a two-person conversation for both users. It asks for a reason and
 * the typed confirmation the server checks. A retry after no answer reuses
 * the attempt's idempotency key, so it can never clear twice; an attempt the
 * server reported as failed gets a new key.
 */
export function ClearTopicDialog({ topic, targetUserId, onClose, onCleared }: ClearTopicDialogProps) {
  const { api, role, strings: s } = useImAdmin();
  const [reason, setReason] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<ClearResult | null>(null);
  const key = useRef<string | null>(null);
  const ids = { title: useId(), reason: useId(), hint: useId(), confirm: useId() };
  if (role !== "superadmin") return null;

  const expected = clearConfirmation(topic);
  const ready = !busy && reason.trim().length > 0 && confirm === expected;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    key.current ??= newKey();
    setBusy(true);
    setError(null);
    try {
      const r = await api.clear(topic, { confirm, reason: reason.trim(), idempotencyKey: key.current, targetUserId });
      setResult(r);
      onCleared?.(r);
    } catch (err) {
      if (err instanceof ImAdminError) {
        setError(err.message);
        // Only "still running" leaves the attempt open; any other answer settled it.
        if (!(err.status === 409 && !err.auditId)) key.current = null;
      } else {
        setError(s.networkError);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="talkwire-admin-backdrop">
      <form
        className="talkwire-admin-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={ids.title}
        onSubmit={submit}
        onKeyDown={(e) => {
          if (e.key === "Escape" && !busy) onClose();
        }}
      >
        <h2 id={ids.title}>{s.clearTitle}</h2>
        <p className="talkwire-admin-warning">{s.clearWarning}</p>
        <p className="talkwire-admin-mono">{topic}</p>
        {result ? (
          <>
            <p role="status">
              {s.cleared(result.deletedMessages, result.deletedMedia)}
              {result.replayed ? ` ${s.clearedBefore}` : ""}
            </p>
            <div className="talkwire-admin-actions">
              <button type="button" onClick={onClose} autoFocus>
                {s.close}
              </button>
            </div>
          </>
        ) : (
          <>
            <label htmlFor={ids.reason}>{s.reason}</label>
            <textarea id={ids.reason} aria-describedby={ids.hint} value={reason} maxLength={500} rows={3} autoFocus onChange={(e) => setReason(e.target.value)} />
            <span id={ids.hint} className="talkwire-admin-muted">
              {s.reasonHint}
            </span>
            <label htmlFor={ids.confirm}>{s.typeToConfirm(expected)}</label>
            <input id={ids.confirm} value={confirm} autoComplete="off" spellCheck={false} onChange={(e) => setConfirm(e.target.value)} />
            {error ? (
              <p role="alert" className="talkwire-admin-error">
                {error}
              </p>
            ) : null}
            <div className="talkwire-admin-actions">
              <button type="button" onClick={onClose} disabled={busy}>
                {s.cancel}
              </button>
              <button type="submit" className="talkwire-admin-danger" disabled={!ready}>
                {busy ? s.clearing : s.clear}
              </button>
            </div>
          </>
        )}
      </form>
    </div>
  );
}

// ------------------------------------------------------------------ call records

interface CallFilter {
  userId: string;
  status: "" | CallStatus;
  endReason: string;
  callType: "" | CallType;
  from: string;
  to: string;
}

export interface CallRecordsProps {
  /** Filter by this user when the page opens. */
  userId?: string;
  /** Opens a call's details; without it the table has no details buttons. */
  onOpen?(callSessionId: string): void;
  pageSize?: number;
}

/** Call records, newest first, with filters and paging. */
export function CallRecords({ userId = "", onOpen, pageSize = 50 }: CallRecordsProps) {
  const { api, strings: s, locale } = useImAdmin();
  const [filter, setFilter] = useState<CallFilter>({ userId, status: "", endReason: "", callType: "", from: "", to: "" });
  const [applied, setApplied] = useState(filter);
  const [rows, setRows] = useState<CallRecord[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const latest = useLatest();
  const ids = { user: useId(), status: useId(), outcome: useId(), type: useId(), from: useId(), to: useId() };

  const query = (f: CallFilter, cursor?: string): CallsQuery => ({
    userId: f.userId.trim() || undefined,
    status: f.status || undefined,
    endReason: f.endReason || undefined,
    callType: f.callType || undefined,
    fromMs: dayStart(f.from),
    toMs: dayEnd(f.to),
    cursor,
    limit: pageSize,
  });

  useEffect(() => {
    const current = latest();
    setRows(null);
    setError(null);
    api.calls(query(applied)).then(
      (r) => {
        if (!current()) return;
        setRows(r.calls);
        setNext(r.nextCursor);
      },
      (err) => {
        if (current()) setError(failure(err, s));
      },
    );
  }, [api, applied, latest, s, pageSize]);

  const more = async () => {
    if (!next || !rows || busy) return;
    const current = latest();
    setBusy(true);
    try {
      const r = await api.calls(query(applied, next));
      if (!current()) return;
      setRows([...rows, ...r.calls]);
      setNext(r.nextCursor);
    } catch (err) {
      if (current()) setError(failure(err, s));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="talkwire-admin-page" aria-label={s.callRecords}>
      <form
        className="talkwire-admin-filters"
        onSubmit={(e) => {
          e.preventDefault();
          setApplied({ ...filter }); // a new object: applying unchanged filters loads again
        }}
      >
        <label htmlFor={ids.user}>{s.userId}</label>
        <input id={ids.user} value={filter.userId} placeholder={s.userIdHint} onChange={(e) => setFilter({ ...filter, userId: e.target.value })} />
        <label htmlFor={ids.status}>{s.status}</label>
        <select id={ids.status} value={filter.status} onChange={(e) => setFilter({ ...filter, status: e.target.value as CallFilter["status"] })}>
          <option value="">{s.allStatuses}</option>
          <option value="ringing">{s.statuses.ringing}</option>
          <option value="active">{s.statuses.active}</option>
          <option value="ended">{s.steps.ended}</option>
        </select>
        <label htmlFor={ids.outcome}>{s.outcome}</label>
        <select id={ids.outcome} value={filter.endReason} onChange={(e) => setFilter({ ...filter, endReason: e.target.value })}>
          <option value="">{s.allOutcomes}</option>
          {Object.entries(s.outcomes).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </select>
        <label htmlFor={ids.type}>{s.type}</label>
        <select id={ids.type} value={filter.callType} onChange={(e) => setFilter({ ...filter, callType: e.target.value as CallFilter["callType"] })}>
          <option value="">{s.allTypes}</option>
          <option value="voice">{s.callTypes.voice}</option>
          <option value="video">{s.callTypes.video}</option>
        </select>
        <label htmlFor={ids.from}>{s.from}</label>
        <input id={ids.from} type="date" value={filter.from} onChange={(e) => setFilter({ ...filter, from: e.target.value })} />
        <label htmlFor={ids.to}>{s.to}</label>
        <input id={ids.to} type="date" value={filter.to} onChange={(e) => setFilter({ ...filter, to: e.target.value })} />
        <button type="submit">{s.apply}</button>
      </form>
      {error ? (
        <p role="alert" className="talkwire-admin-error">
          {error}
        </p>
      ) : null}
      {rows === null ? (
        error ? null : <p className="talkwire-admin-muted">{s.loading}</p>
      ) : rows.length === 0 ? (
        <p className="talkwire-admin-muted">{s.noCalls}</p>
      ) : (
        <div className="talkwire-admin-scroll">
          <table className="talkwire-admin-table">
            <thead>
              <tr>
                <th scope="col">{s.time}</th>
                <th scope="col">{s.caller}</th>
                <th scope="col">{s.callee}</th>
                <th scope="col">{s.type}</th>
                <th scope="col">{s.status}</th>
                <th scope="col">{s.duration}</th>
                {onOpen ? (
                  <th scope="col">
                    <span className="talkwire-admin-hidden">{s.open}</span>
                  </th>
                ) : null}
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.callSessionId} data-call-id={r.callSessionId}>
                  <td>{formatTime(r.createdAt, locale)}</td>
                  <td>
                    <PersonLine p={r.caller} />
                  </td>
                  <td>
                    <PersonLine p={r.callee} />
                  </td>
                  <td>{callTypeText(r, s)}</td>
                  <td>
                    <span className={`talkwire-admin-status talkwire-admin-status-${callTone(r)}`}>{callStatusText(r, s)}</span>
                  </td>
                  <td>{r.durationSec === null ? "—" : formatDuration(r.durationSec)}</td>
                  {onOpen ? (
                    <td>
                      <button type="button" onClick={() => onOpen(r.callSessionId)}>
                        {s.open}
                      </button>
                    </td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {next && rows?.length ? (
        <button type="button" className="talkwire-admin-more" disabled={busy} onClick={() => void more()}>
          {busy ? s.loading : s.loadMore}
        </button>
      ) : null}
    </section>
  );
}

export interface CallDetailProps {
  callSessionId: string;
  onBack?(): void;
}

/** One call: who, what kind, how it ended, and when each step happened. */
export function CallDetail({ callSessionId, onBack }: CallDetailProps) {
  const { api, strings: s, locale } = useImAdmin();
  const [call, setCall] = useState<CallRecord | null>(null);
  const [error, setError] = useState<string | null>(null);
  const latest = useLatest();

  useEffect(() => {
    const current = latest();
    setCall(null);
    setError(null);
    api.call(callSessionId).then(
      (c) => {
        if (current()) setCall(c);
      },
      (err) => {
        if (current()) setError(err instanceof ImAdminError && err.status === 404 ? s.notFound : failure(err, s));
      },
    );
  }, [api, callSessionId, latest, s]);

  const steps: [keyof AdminStrings["steps"], string | null][] = call
    ? [
        ["created", call.createdAt],
        ["answered", call.answeredAt],
        ["upgraded", call.upgradedAt],
        ["ended", call.endedAt],
      ]
    : [];

  return (
    <section className="talkwire-admin-page" aria-label={s.callDetail}>
      {onBack ? (
        <button type="button" className="talkwire-admin-back" onClick={onBack}>
          ← {s.back}
        </button>
      ) : null}
      {error ? (
        <p role="alert" className="talkwire-admin-error">
          {error}
        </p>
      ) : !call ? (
        <p className="talkwire-admin-muted">{s.loading}</p>
      ) : (
        <>
          <h2>
            {personName(call.caller, s)} → {personName(call.callee, s)}
          </h2>
          <dl className="talkwire-admin-fields">
            <dt>{s.callId}</dt>
            <dd className="talkwire-admin-mono">{call.callSessionId}</dd>
            <dt>{s.caller}</dt>
            <dd>
              <PersonLine p={call.caller} /> <span className="talkwire-admin-muted talkwire-admin-mono">{call.caller.uid}</span>
            </dd>
            <dt>{s.callee}</dt>
            <dd>
              <PersonLine p={call.callee} /> <span className="talkwire-admin-muted talkwire-admin-mono">{call.callee.uid}</span>
            </dd>
            <dt>{s.type}</dt>
            <dd>
              {s.callTypes[call.callType]}
              {call.requestedType !== call.callType ? ` (${s.requested} ${s.callTypes[call.requestedType]})` : ""}
            </dd>
            <dt>{s.status}</dt>
            <dd>
              <span className={`talkwire-admin-status talkwire-admin-status-${callTone(call)}`}>{callStatusText(call, s)}</span>
            </dd>
            <dt>{s.duration}</dt>
            <dd>{call.durationSec === null ? "—" : formatDuration(call.durationSec)}</dd>
          </dl>
          <h3>{s.timeline}</h3>
          <ol className="talkwire-admin-timeline">
            {steps
              .filter((x): x is [keyof AdminStrings["steps"], string] => x[1] !== null)
              .map(([step, at]) => (
                <li key={step}>
                  <time dateTime={at}>{formatTime(at, locale)}</time> {s.steps[step]}
                </li>
              ))}
          </ol>
        </>
      )}
    </section>
  );
}

// ------------------------------------------------------------------ styles

export const adminCss = `
.talkwire-admin { font-family: var(--talkwire-font, system-ui, sans-serif); color: var(--talkwire-text, #111827); }
.talkwire-admin button, .talkwire-admin input, .talkwire-admin select, .talkwire-admin textarea { font: inherit; }
.talkwire-admin button { border: 1px solid var(--talkwire-border, #e5e7eb); background: var(--talkwire-surface, #fff); color: inherit; border-radius: 8px; padding: 6px 12px; cursor: pointer; }
.talkwire-admin button:disabled { opacity: .5; cursor: default; }
.talkwire-admin input, .talkwire-admin select, .talkwire-admin textarea { border: 1px solid var(--talkwire-border, #e5e7eb); border-radius: 8px; padding: 6px 8px; background: var(--talkwire-surface, #fff); color: inherit; }
.talkwire-admin .talkwire-admin-danger { background: var(--talkwire-danger, #dc2626); border-color: transparent; color: #fff; }
.talkwire-admin-page { display: flex; flex-direction: column; gap: 12px; }
.talkwire-admin-filters { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.talkwire-admin-muted { color: var(--talkwire-text-muted, #6b7280); }
.talkwire-admin-mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .9em; overflow-wrap: anywhere; }
.talkwire-admin-error { color: var(--talkwire-danger, #dc2626); }
.talkwire-admin-warning { color: var(--talkwire-danger, #dc2626); font-weight: 600; }
.talkwire-admin-tag { display: inline-block; font-size: 12px; padding: 0 6px; border-radius: 999px; background: var(--talkwire-bg, #f3f4f6); color: var(--talkwire-text-muted, #6b7280); }
.talkwire-admin-split { display: grid; grid-template-columns: minmax(220px, 320px) 1fr; gap: 12px; align-items: start; }
@media (max-width: 720px) { .talkwire-admin-split { grid-template-columns: 1fr; } }
.talkwire-admin-topics { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; }
.talkwire-admin-topics button { width: 100%; text-align: left; display: flex; flex-direction: column; gap: 2px; }
.talkwire-admin-topics button[aria-current="true"] { border-color: var(--talkwire-accent, #2563eb); }
.talkwire-admin-topic-title { font-weight: 600; }
.talkwire-admin-bar { display: flex; justify-content: space-between; align-items: center; gap: 8px; }
.talkwire-admin-messages { display: flex; flex-direction: column; gap: 10px; }
.talkwire-admin-message-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
.talkwire-admin-message-list li { border: 1px solid var(--talkwire-border, #e5e7eb); border-radius: 8px; padding: 8px 10px; background: var(--talkwire-surface, #fff); }
.talkwire-admin-message-head { display: flex; justify-content: space-between; gap: 8px; font-size: 13px; }
.talkwire-admin-text { margin: 4px 0 0; white-space: pre-wrap; overflow-wrap: anywhere; }
.talkwire-admin-person { display: inline-flex; flex-direction: column; }
.talkwire-admin-scroll { overflow-x: auto; }
.talkwire-admin-table { border-collapse: collapse; width: 100%; font-size: 14px; }
.talkwire-admin-table th, .talkwire-admin-table td { text-align: left; padding: 8px; border-bottom: 1px solid var(--talkwire-border, #e5e7eb); vertical-align: top; }
.talkwire-admin-status { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: 12px; background: var(--talkwire-bg, #f3f4f6); }
.talkwire-admin-status-live { background: #dbeafe; color: #1d4ed8; }
.talkwire-admin-status-ok { background: #dcfce7; color: #166534; }
.talkwire-admin-status-bad { background: #fee2e2; color: #991b1b; }
.talkwire-admin-fields { display: grid; grid-template-columns: max-content 1fr; gap: 6px 16px; margin: 0; }
.talkwire-admin-fields dd { margin: 0; }
.talkwire-admin-timeline { margin: 0; padding-left: 20px; display: flex; flex-direction: column; gap: 4px; }
.talkwire-admin-backdrop { position: fixed; inset: 0; background: rgb(0 0 0 / .4); display: flex; align-items: center; justify-content: center; z-index: 1000; padding: 16px; }
.talkwire-admin-dialog { background: var(--talkwire-surface, #fff); color: var(--talkwire-text, #111827); border-radius: 12px; padding: 20px; width: min(480px, 100%); display: flex; flex-direction: column; gap: 8px; }
.talkwire-admin-dialog h2 { margin: 0; font-size: 18px; }
.talkwire-admin-dialog p { margin: 0; }
.talkwire-admin-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 8px; }
.talkwire-admin-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
`;

/** Puts the pages' stylesheet in the page once. */
export function AdminStyles() {
  return <style data-talkwire-admin="">{adminCss}</style>;
}
