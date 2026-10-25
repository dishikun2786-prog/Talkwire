import { AI_ASSISTANT_UID, type ChatEngine, type ConnectionState, type ConversationId, type ConversationSummary, type MediaSender, type PresenceState, type ReadonlySignal } from "@imkit/client";
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { MessageTypeRegistry } from "./registry.js";
import { en, stringsFor, type ImStrings } from "./strings.js";

/** A person as the host knows them; the kit itself only knows uids. */
export interface ImProfile {
  uid: string;
  name: string;
  avatarUrl?: string;
}

/**
 * Everything the components need from the app (the web counterpart of the
 * Android `ImUiHost`): who people are, the user's contacts, and media.
 */
export interface ImUiHost {
  /** Names and avatars for `uids`; missing ones fall back to a short uid. */
  profiles(uids: string[]): Promise<ImProfile[]>;
  /** The user's contacts, for new chats and new groups. */
  contacts?: ReadonlySignal<ImProfile[]>;
  /** Reloads `contacts`, for example when a picker opens. */
  refreshContacts?(): Promise<void>;
  /** Uploads and sends media; without it the attach and voice buttons are hidden. */
  media?: MediaSender;
  /** Makes a stored media URL loadable. Default: unchanged. */
  resolveMediaUrl?(url: string | undefined): string | undefined;
  /** A tap on someone's avatar. Default: nothing (group members cannot open each other). */
  onOpenProfile?(uid: string): void;
}

interface ImKitValue {
  engine: ChatEngine;
  host: ImUiHost;
  strings: ImStrings;
  /** The provider's `locale`, "en" when it has none: module components pick their own tables by it. */
  locale: string;
  types: MessageTypeRegistry;
  profiles: ProfileCache;
}

const ImKitContext = createContext<ImKitValue | null>(null);

export interface ImKitProviderProps {
  engine: ChatEngine;
  host: ImUiHost;
  /** A BCP 47 tag picking a built-in table (en, zh, vi), and/or single texts to replace. */
  locale?: string;
  strings?: Partial<ImStrings>;
  types?: MessageTypeRegistry;
  children: ReactNode;
}

export function ImKitProvider({ engine, host, locale, strings, types, children }: ImKitProviderProps) {
  const resolved = useMemo(() => ({ ...(locale ? stringsFor(locale) : en), ...strings }), [locale, strings]);
  // Until the host names someone, the group assistant goes by strings.assistant.
  const assistant = useLatest(resolved.assistant);
  const profiles = useMemo(() => new ProfileCache(host, (uid) => (uid === AI_ASSISTANT_UID ? assistant.current : shortUid(uid))), [host, assistant]);
  const value = useMemo<ImKitValue>(
    () => ({ engine, host, profiles, types: types ?? MessageTypeRegistry.Default, strings: resolved, locale: locale ?? "en" }),
    [engine, host, profiles, types, resolved, locale],
  );
  return <ImKitContext.Provider value={value}>{children}</ImKitContext.Provider>;
}

export function useImKit(): ImKitValue {
  const v = useContext(ImKitContext);
  if (!v) throw new Error("ImKit components need an <ImKitProvider>");
  return v;
}

/** Subscribes a component to a signal. */
export function useSignal<T>(signal: ReadonlySignal<T>): T {
  return useSyncExternalStore(
    useCallback((cb: () => void) => signal.subscribe(cb), [signal]),
    () => signal.value,
    () => signal.value,
  );
}

export function useConnectionState(): ConnectionState {
  return useSignal(useImKit().engine.client.state);
}

export function useConversations(): ConversationSummary[] {
  return useSignal(useImKit().engine.conversations);
}

export function useConversation(id: ConversationId): ConversationSummary | undefined {
  return useConversations().find((c) => c.id === id);
}

export function useMessages(conversation: ConversationId) {
  const { engine } = useImKit();
  return useSignal(useMemo(() => engine.messages(conversation), [engine, conversation]));
}

/** Presence of a user, from the engine's one tracker (C5). */
export function usePresence(uid: string | undefined): PresenceState {
  const { engine } = useImKit();
  useSignal(engine.presence.version);
  return uid ? engine.presence.state(uid) : "unknown";
}

export function useTyping(conversation: ConversationId): string[] {
  const { engine } = useImKit();
  useSignal(engine.typing);
  return engine.typingIn(conversation);
}

/**
 * A person's profile; a short uid until the host answers. Read through
 * `useSyncExternalStore`, so an answer that lands between the render and the
 * subscription is not missed.
 */
export function useProfile(uid: string | undefined): ImProfile | undefined {
  const { profiles } = useImKit();
  const known = useSyncExternalStore(
    useCallback((cb: () => void) => (uid ? profiles.watch(uid, cb) : () => {}), [profiles, uid]),
    () => (uid ? profiles.peek(uid) : undefined),
  );
  return uid ? (known ?? profiles.get(uid)) : undefined;
}

/**
 * Re-renders when any profile arrives, for components that show many people
 * (a member list); read the names with `profiles.get`.
 */
export function useProfilesVersion(): number {
  const { profiles } = useImKit();
  return useSyncExternalStore(
    useCallback((cb: () => void) => profiles.watchAll(cb), [profiles]),
    () => profiles.version,
  );
}

/** A shortened uid for people the host has not named yet. */
export const shortUid = (uid: string): string => (uid.startsWith("uid_") ? uid.slice(4, 10) : uid.slice(0, 6));

/** Batches profile lookups made in the same tick and caches the answers. */
export class ProfileCache {
  private readonly known = new Map<string, ImProfile>();
  private readonly listeners = new Map<string, Set<() => void>>();
  private readonly anyListeners = new Set<() => void>();
  private _version = 0;
  private readonly queued = new Set<string>();
  private readonly asked = new Set<string>();
  private timer: ReturnType<typeof setTimeout> | null = null;

  /** fallbackName names someone the host has not named (yet). */
  constructor(
    private readonly host: ImUiHost,
    private readonly fallbackName: (uid: string) => string = shortUid,
  ) {}

  get(uid: string): ImProfile {
    const p = this.known.get(uid);
    if (p) return p;
    if (!this.asked.has(uid)) {
      this.asked.add(uid);
      this.queued.add(uid);
      this.timer ??= setTimeout(() => this.flush(), 0);
    }
    return { uid, name: this.fallbackName(uid) };
  }

  /** The profile the host gave, without asking for it; undefined when there is none yet. */
  peek(uid: string): ImProfile | undefined {
    return this.known.get(uid);
  }

  /** Grows each time profiles arrive. */
  get version(): number {
    return this._version;
  }

  /** Puts profiles known from elsewhere (the contact list) into the cache. */
  put(profiles: ImProfile[]): void {
    if (profiles.length === 0) return;
    for (const p of profiles) this.known.set(p.uid, p);
    this._version++;
    for (const p of profiles) this.listeners.get(p.uid)?.forEach((l) => l());
    this.anyListeners.forEach((l) => l());
  }

  /** Called whenever any profile arrives. */
  watchAll(listener: () => void): () => void {
    this.anyListeners.add(listener);
    return () => void this.anyListeners.delete(listener);
  }

  watch(uid: string, listener: () => void): () => void {
    let set = this.listeners.get(uid);
    if (!set) this.listeners.set(uid, (set = new Set()));
    set.add(listener);
    return () => set!.delete(listener);
  }

  private flush(): void {
    this.timer = null;
    const uids = [...this.queued];
    this.queued.clear();
    if (uids.length === 0) return;
    this.host.profiles(uids).then(
      (found) => this.put(found),
      () => uids.forEach((u) => this.asked.delete(u)), // ask again next time
    );
  }
}

/** Runs `fn` once per mount with the latest props, ignoring re-renders. */
export function useLatest<T>(value: T) {
  const ref = useRef(value);
  ref.current = value;
  return ref;
}
