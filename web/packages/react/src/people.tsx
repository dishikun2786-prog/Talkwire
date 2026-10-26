import { canManage, isGroupConversation, isOk, type ConversationId, type GroupMember, type GroupRole, type Reply } from "@talkwire/client";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { shortUid, useConversation, useTalkwire, useProfile, useProfilesVersion, useSignal, type ImProfile } from "./context.js";
import { Avatar } from "./components.js";
import { ConfirmDialog, Dialog, PopupMenu, useToast } from "./dialogs.js";
import { format } from "./strings.js";

/** The longest group name the server takes (group_chat.name_max_len). */
export const GROUP_NAME_MAX = 30;

/** What `me` may do to `target` in a group. Mirrors the server's checks, so the UI does not offer what it will refuse. */
export interface MemberPermissions {
  setRole: boolean;
  kick: boolean;
  transfer: boolean;
}

export function memberPermissions(me: GroupRole, myUid: string, target: GroupMember): MemberPermissions {
  if (target.uid === myUid || target.role === "owner") return { setRole: false, kick: false, transfer: false };
  return {
    setRole: me === "owner",
    // Admins remove members but not other admins.
    kick: me === "owner" || (me === "admin" && target.role === "member"),
    transfer: me === "owner",
  };
}

const RANK: Record<GroupRole, number> = { owner: 0, admin: 1, member: 2 };

/** Owners first, then admins, then members by when they joined. */
export function sortMembers(members: readonly GroupMember[]): GroupMember[] {
  return [...members].sort((a, b) => RANK[a.role] - RANK[b.role] || a.joinedAt - b.joinedAt);
}

/** A default name for a new group: the members' names, cut to fit. */
export function defaultGroupName(names: readonly string[]): string {
  const joined = names.join(", ");
  return joined.length <= GROUP_NAME_MAX ? joined : joined.slice(0, GROUP_NAME_MAX - 1).replace(/[, ]+$/, "") + "…";
}

// ------------------------------------------------------------------ conversation info

export interface ConversationInfoProps {
  conversation: ConversationId;
  onBack?(): void;
  /** After leaving or disbanding the group; usually back to the list. */
  onLeft?(): void;
  /** Adding members; without it the button opens the kit's own picker. */
  onAddMembers?(): void;
  /**
   * Sections an optional module adds to a group's info, after the members and
   * before the chat settings (the group-join module's QR code and join
   * requests, say). `role` is the user's own role in the group.
   */
  groupExtras?: (topic: string, role: GroupRole) => ReactNode;
  /** The most admins a group may have (the server's group_chat.max_admins). */
  maxAdmins?: number;
}

type Pending =
  | { kind: "kick" | "transfer"; uid: string }
  | { kind: "leave" | "disband" | "clear" | "rename" | "add" };

/**
 * A conversation's settings: for a group the members and their roles, what
 * the user may do to each, renaming and leaving; for both pin, mute and
 * clear. The web counterpart of the Android kit's info screen.
 */
export function ConversationInfo({ conversation, onBack, onLeft, onAddMembers, groupExtras, maxAdmins = 10 }: ConversationInfoProps) {
  const { engine, strings, host, profiles } = useTalkwire();
  const summary = useConversation(conversation);
  const isGroup = isGroupConversation(conversation);
  const peer = useProfile(isGroup ? undefined : conversation);
  const [members, setMembers] = useState<GroupMember[]>([]);
  const [pending, setPending] = useState<Pending | null>(null);
  const [menu, setMenu] = useState<{ uid: string; x: number; y: number } | null>(null);
  const [toast, showToast] = useToast();
  useProfilesVersion();

  const myRole: GroupRole = members.find((m) => m.uid === engine.me)?.role ?? "member";
  const title = isGroup ? (summary?.title ?? shortUid(conversation)) : (peer?.name ?? shortUid(conversation));

  const reload = useCallback(async () => {
    const list = await engine.members(conversation, true);
    if (!list) return;
    setMembers(sortMembers(list));
  }, [engine, conversation]);

  useEffect(() => {
    if (!isGroup) return;
    void reload();
    return engine.events.on((e) => {
      if (e.kind === "groupChanged" && e.conversation === conversation) void reload();
    });
  }, [engine, conversation, isGroup, reload]);

  const nameOf = (uid: string) => profiles.get(uid).name;

  /** Runs a group request; a refused or failed one shows a message and keeps the page as it is. */
  const act = async (call: () => Promise<Reply>, onDone: () => Promise<void> | void = reload) => {
    let reply: Reply | null = null;
    try {
      reply = await call();
    } catch {
      reply = null;
    }
    if (reply && isOk(reply)) await onDone();
    else showToast(reply?.code === 409 ? strings.adminLimit : strings.actionFailed);
  };
  const left = async () => {
    await engine.refreshGroups().catch(() => null);
    onLeft?.();
  };

  const menuMember = menu ? members.find((m) => m.uid === menu.uid) : undefined;
  const menuPerms = menuMember ? memberPermissions(myRole, engine.me, menuMember) : null;

  return (
    <section className="talkwire-info" aria-label={isGroup ? strings.groupInfo : strings.chatInfo} style={{ position: "relative" }}>
      <header className="talkwire-chat-header">
        {onBack && (
          <button type="button" className="talkwire-icon-button" onClick={onBack} aria-label={strings.back}>
            ←
          </button>
        )}
        <div className="talkwire-chat-title" style={{ flex: 1 }}>{isGroup ? strings.groupInfo : strings.chatInfo}</div>
      </header>
      <div className="talkwire-info-body">
        <div className="talkwire-info-head">
          <Avatar uid={conversation} name={title} url={isGroup ? undefined : peer?.avatarUrl} size={56} />
          <span className="talkwire-info-title">{title}</span>
        </div>

        {isGroup && (
          <>
            <div className="talkwire-info-caption">{format(strings.membersCount, members.length)}</div>
            <div className="talkwire-info-group" role="list" aria-label={format(strings.membersCount, members.length)}>
              {members.map((m) => {
                const perms = memberPermissions(myRole, engine.me, m);
                const any = perms.setRole || perms.kick || perms.transfer;
                const name = m.uid === engine.me ? strings.you : nameOf(m.uid);
                const profile = profiles.get(m.uid);
                const row = (
                  <>
                    <Avatar uid={m.uid} name={profile.name} url={host.resolveMediaUrl ? host.resolveMediaUrl(profile.avatarUrl) : profile.avatarUrl} size={40} />
                    <span className="talkwire-info-name">{name}</span>
                    {m.role !== "member" && <span className="talkwire-role">{m.role === "owner" ? strings.roleOwner : strings.roleAdmin}</span>}
                  </>
                );
                // Rows open nothing for plain members: members cannot view each other's profiles.
                return any ? (
                  <button key={m.uid} type="button" role="listitem" className="talkwire-info-row" data-member={m.uid} onClick={(e) => setMenu({ uid: m.uid, x: e.clientX, y: e.clientY })}>
                    {row}
                  </button>
                ) : (
                  <div key={m.uid} role="listitem" className="talkwire-info-row" data-member={m.uid}>
                    {row}
                  </div>
                );
              })}
              <button type="button" className="talkwire-info-row talkwire-info-accent" onClick={() => (onAddMembers ? onAddMembers() : setPending({ kind: "add" }))}>
                <span className="talkwire-info-plus" aria-hidden="true">＋</span>
                {strings.addMembers}
              </button>
            </div>
            <p className="talkwire-info-hint">{strings.membersPrivate}</p>
            {canManage(myRole) && (
              <div className="talkwire-info-group">
                <button type="button" className="talkwire-info-row" onClick={() => setPending({ kind: "rename" })}>{strings.renameGroup}</button>
              </div>
            )}
            {groupExtras?.(conversation, myRole)}
          </>
        )}

        <div className="talkwire-info-group">
          <Toggle label={strings.pinChat} on={summary?.pinned === true} onChange={(on) => void engine.setPinned(conversation, on).catch(() => showToast(strings.actionFailed))} />
          <Toggle label={strings.muteNotifications} on={summary?.muted === true} onChange={(on) => void engine.setMuted(conversation, on).catch(() => showToast(strings.actionFailed))} />
        </div>
        <div className="talkwire-info-group">
          {/* In groups only owners and admins clear for everyone; the server checks it too. */}
          {(!isGroup || canManage(myRole)) && (
            <button type="button" className="talkwire-info-row talkwire-danger-text" onClick={() => setPending({ kind: "clear" })}>{strings.clearHistory}</button>
          )}
          {isGroup &&
            (myRole === "owner" ? (
              <button type="button" className="talkwire-info-row talkwire-danger-text" onClick={() => setPending({ kind: "disband" })}>{strings.disbandGroup}</button>
            ) : (
              <button type="button" className="talkwire-info-row talkwire-danger-text" onClick={() => setPending({ kind: "leave" })}>{strings.leaveGroup}</button>
            ))}
        </div>
      </div>

      {menu && menuMember && menuPerms && (
        <PopupMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)}>
          {menuPerms.setRole && (
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setMenu(null);
                const toAdmin = menuMember.role === "member";
                if (toAdmin && members.filter((m) => m.role === "admin").length >= maxAdmins) showToast(strings.adminLimit);
                else void act(() => engine.client.setMemberRole(conversation, menuMember.uid, toAdmin ? "admin" : "member"));
              }}
            >
              {menuMember.role === "member" ? strings.makeAdmin : strings.removeAdmin}
            </button>
          )}
          {menuPerms.transfer && (
            <button type="button" role="menuitem" onClick={() => { setMenu(null); setPending({ kind: "transfer", uid: menuMember.uid }); }}>
              {strings.transferOwner}
            </button>
          )}
          {menuPerms.kick && (
            <button type="button" role="menuitem" className="talkwire-danger-text" onClick={() => { setMenu(null); setPending({ kind: "kick", uid: menuMember.uid }); }}>
              {strings.removeMember}
            </button>
          )}
        </PopupMenu>
      )}

      {pending?.kind === "kick" && (
        <ConfirmDialog message={format(strings.removeConfirm, nameOf(pending.uid))} action={strings.removeMember} onClose={() => setPending(null)} onConfirm={() => void act(() => engine.client.kick(conversation, pending.uid))} />
      )}
      {pending?.kind === "transfer" && (
        <ConfirmDialog message={format(strings.transferConfirm, nameOf(pending.uid))} action={strings.transferOwner} onClose={() => setPending(null)} onConfirm={() => void act(() => engine.client.transferOwner(conversation, pending.uid))} />
      )}
      {pending?.kind === "leave" && (
        <ConfirmDialog message={strings.leaveConfirm} action={strings.leaveGroup} onClose={() => setPending(null)} onConfirm={() => void act(() => engine.client.leaveGroup(conversation), left)} />
      )}
      {pending?.kind === "disband" && (
        <ConfirmDialog message={strings.disbandConfirm} action={strings.disbandGroup} onClose={() => setPending(null)} onConfirm={() => void act(() => engine.client.disbandGroup(conversation), left)} />
      )}
      {pending?.kind === "clear" && (
        <ConfirmDialog message={strings.clearHistoryConfirm} action={strings.clearHistory} onClose={() => setPending(null)} onConfirm={() => void engine.clear(conversation).catch(() => showToast(strings.actionFailed))} />
      )}
      {pending?.kind === "rename" && (
        <RenameDialog
          initial={title}
          onClose={() => setPending(null)}
          onSave={(name) => void act(() => engine.client.renameGroup(conversation, name), async () => void (await engine.refreshGroups().catch(() => null)))}
        />
      )}
      {pending?.kind === "add" && (
        <Dialog label={strings.addMembers} onClose={() => setPending(null)}>
          <PickPeople topic={conversation} onDone={() => { setPending(null); void reload(); }} onCancel={() => setPending(null)} />
        </Dialog>
      )}
      {toast && <div className="talkwire-toast" role="alert">{toast}</div>}
    </section>
  );
}

function Toggle({ label, on, onChange }: { label: string; on: boolean; onChange(on: boolean): void }) {
  return (
    <label className="talkwire-info-row talkwire-toggle">
      <span style={{ flex: 1 }}>{label}</span>
      <input type="checkbox" role="switch" checked={on} onChange={(e) => onChange(e.target.checked)} />
    </label>
  );
}

function RenameDialog({ initial, onSave, onClose }: { initial: string; onSave(name: string): void; onClose(): void }) {
  const { strings } = useTalkwire();
  const [name, setName] = useState(initial);
  return (
    <Dialog label={strings.renameGroup} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!name.trim()) return;
          onClose();
          onSave(name.trim().slice(0, GROUP_NAME_MAX));
        }}
      >
        <h3 className="talkwire-dialog-title">{strings.renameGroup}</h3>
        <input className="talkwire-field" aria-label={strings.groupName} value={name} maxLength={GROUP_NAME_MAX} onChange={(e) => setName(e.target.value)} autoFocus />
        <div className="talkwire-dialog-actions">
          <button type="button" className="talkwire-text-button" onClick={onClose}>{strings.cancel}</button>
          <button type="submit" className="talkwire-text-button" disabled={!name.trim()}>{strings.save}</button>
        </div>
      </form>
    </Dialog>
  );
}

// ------------------------------------------------------------------ picking people

export interface PickPeopleProps {
  /** The group to add people to; without it the picked people start a new group. */
  topic?: string;
  /** After the group was created (its conversation) or the people were added. */
  onDone(conversation: ConversationId): void;
  onCancel?(): void;
}

/**
 * Picks contacts from the host for a new group, or to add to `topic`. People
 * already in the group are shown but cannot be picked.
 */
export function PickPeople({ topic, onDone, onCancel }: PickPeopleProps) {
  const { engine, strings, host } = useTalkwire();
  const contacts = useContacts();
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  const [existing, setExisting] = useState<ReadonlySet<string>>(new Set());
  const [groupName, setGroupName] = useState("");
  const [busy, setBusy] = useState(false);
  const [toast, showToast] = useToast();

  useEffect(() => {
    if (!topic) return;
    let live = true;
    void engine.members(topic, true).then((m) => live && m && setExisting(new Set(m.map((x) => x.uid))));
    return () => {
      live = false;
    };
  }, [engine, topic]);

  const q = query.trim().toLowerCase();
  const shown = contacts.filter((c) => c.uid !== engine.me && (!q || c.name.toLowerCase().includes(q))).sort((a, b) => a.name.localeCompare(b.name));

  const submit = async () => {
    if (picked.size === 0 || busy) return;
    setBusy(true);
    try {
      if (!topic) {
        const names = contacts.filter((c) => picked.has(c.uid)).map((c) => c.name);
        const name = (groupName.trim() || defaultGroupName(names)).slice(0, GROUP_NAME_MAX);
        onDone(await engine.createGroup(name, [...picked]));
      } else {
        let failed = 0;
        for (const uid of picked) {
          const reply = await engine.client.invite(topic, uid).catch(() => null);
          if (!reply || !isOk(reply)) failed++;
        }
        await engine.members(topic, true);
        if (failed > 0) showToast(strings.actionFailed);
        else onDone(topic);
      }
    } catch {
      showToast(strings.actionFailed);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="talkwire-pick" style={{ position: "relative" }}>
      <h3 className="talkwire-dialog-title">{topic ? strings.addMembers : strings.createGroup}</h3>
      {!topic && (
        <input className="talkwire-field" aria-label={strings.groupName} placeholder={strings.groupName} value={groupName} maxLength={GROUP_NAME_MAX} onChange={(e) => setGroupName(e.target.value)} />
      )}
      <input className="talkwire-field" type="search" aria-label={strings.search} placeholder={strings.search} value={query} onChange={(e) => setQuery(e.target.value)} />
      <div className="talkwire-pick-list" role="list">
        {shown.length === 0 ? (
          <div className="talkwire-list-empty">{strings.noContacts}</div>
        ) : (
          shown.map((c) => {
            const already = existing.has(c.uid);
            return (
              <label key={c.uid} className="talkwire-info-row" role="listitem" data-contact={c.uid} aria-disabled={already}>
                <input
                  type="checkbox"
                  checked={already || picked.has(c.uid)}
                  disabled={already}
                  onChange={(e) => {
                    const next = new Set(picked);
                    if (e.target.checked) next.add(c.uid);
                    else next.delete(c.uid);
                    setPicked(next);
                  }}
                />
                <Avatar uid={c.uid} name={c.name} url={host.resolveMediaUrl ? host.resolveMediaUrl(c.avatarUrl) : c.avatarUrl} size={36} />
                <span className="talkwire-info-name">{c.name}</span>
              </label>
            );
          })
        )}
      </div>
      <div className="talkwire-dialog-actions">
        {onCancel && <button type="button" className="talkwire-text-button" onClick={onCancel}>{strings.cancel}</button>}
        <button type="button" className="talkwire-send" disabled={picked.size === 0 || busy} onClick={() => void submit()}>
          {format(topic ? strings.addCount : strings.createCount, picked.size)}
        </button>
      </div>
      {toast && <div className="talkwire-toast" role="alert">{toast}</div>}
    </div>
  );
}

const NO_CONTACTS: ImProfile[] = [];
const emptyContacts = { value: NO_CONTACTS, subscribe: () => () => {} };

/** The host's contacts (refreshed when a picker opens), also put into the profile cache. */
export function useContacts(): ImProfile[] {
  const { host, profiles } = useTalkwire();
  const contacts = useSignal(host.contacts ?? emptyContacts);
  useEffect(() => {
    void host.refreshContacts?.().catch(() => {});
  }, [host]);
  useEffect(() => profiles.put(contacts), [profiles, contacts]);
  return contacts;
}

// ------------------------------------------------------------------ contacts

export interface ContactListProps {
  /** A contact was picked: the single chat with them exists by then. */
  onOpen(conversation: ConversationId): void;
}

/** The host's contacts with a search box; a click starts (or opens) the single chat. */
export function ContactList({ onOpen }: ContactListProps) {
  const { engine, strings, host } = useTalkwire();
  const contacts = useContacts();
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const shown = contacts.filter((c) => c.uid !== engine.me && (!q || c.name.toLowerCase().includes(q))).sort((a, b) => a.name.localeCompare(b.name));
  return (
    <div className="talkwire-contacts">
      <input className="talkwire-field" type="search" aria-label={strings.search} placeholder={strings.search} value={query} onChange={(e) => setQuery(e.target.value)} />
      <div className="talkwire-contact-list" role="list" aria-label={strings.contacts}>
        {shown.length === 0 ? (
          <div className="talkwire-list-empty">{strings.noContacts}</div>
        ) : (
          shown.map((c) => (
            <button
              key={c.uid}
              type="button"
              role="listitem"
              className="talkwire-info-row"
              data-contact={c.uid}
              onClick={async () => {
                await engine.ensureConversation(c.uid, c.name).catch(() => {});
                onOpen(c.uid);
              }}
            >
              <Avatar uid={c.uid} name={c.name} url={host.resolveMediaUrl ? host.resolveMediaUrl(c.avatarUrl) : c.avatarUrl} size={36} />
              <span className="talkwire-info-name">{c.name}</span>
            </button>
          ))
        )}
      </div>
    </div>
  );
}
