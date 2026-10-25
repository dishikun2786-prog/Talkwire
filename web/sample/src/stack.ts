/**
 * Everything the sample builds after sign-in: the client (through a tab
 * transport, so tabs of one account share a connection), the engine on an
 * IndexedDB store, the host adapter and media.
 */

import { ChatEngine, ImClient, MediaSender, MediaUploader, Signal, createTabTransport, type TabTransport } from "@imkit/client";
import type { ImProfile, ImUiHost } from "@imkit/react";
import { IdbChatStore } from "@imkit/storage-idb";
// One SharedWorker per account holds the WebSocket for all of its tabs.
import ImkitWorker from "./imkit-worker.ts?sharedworker";

export interface Session {
  userId: string;
  uid: string;
  name: string;
  accessToken: string;
}

const SESSION_KEY = "imkit-sample-session";
const DEVICE_KEY = "imkit-sample-device";

export function savedSession(): Session | null {
  try {
    const raw = localStorage.getItem(SESSION_KEY);
    return raw ? (JSON.parse(raw) as Session) : null;
  } catch {
    return null;
  }
}

export function saveSession(s: Session | null): void {
  try {
    if (s) localStorage.setItem(SESSION_KEY, JSON.stringify(s));
    else localStorage.removeItem(SESSION_KEY);
  } catch {
    // private mode: the session lasts as long as the page
  }
}

/** This browser profile's device name for multi-device login (section 6.2). */
function deviceName(): string {
  try {
    let d = localStorage.getItem(DEVICE_KEY);
    if (!d) {
      d = "web-" + crypto.randomUUID().slice(0, 8);
      localStorage.setItem(DEVICE_KEY, d);
    }
    return d;
  } catch {
    return "web-" + crypto.randomUUID().slice(0, 8);
  }
}

export async function devLogin(name: string): Promise<Session> {
  const resp = await fetch("/dev/login", { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ name }) });
  const body = (await resp.json().catch(() => ({}))) as Partial<Session> & { error?: string };
  if (!resp.ok || !body.accessToken) throw new Error(body.error ?? `sign-in failed (${resp.status})`);
  return body as Session;
}

export interface Stack {
  session: Session;
  client: ImClient;
  engine: ChatEngine;
  host: ImUiHost & { contacts: Signal<ImProfile[]> };
  tabs: TabTransport;
  refreshContacts(): Promise<void>;
  close(): void;
}

export async function buildStack(session: Session): Promise<Stack> {
  const token = () => session.accessToken;
  const wsUrl = `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws/im`;
  const tabs = createTabTransport({
    name: session.userId,
    sharedWorker:
      typeof SharedWorker === "function" && !new URLSearchParams(location.search).has("noworker")
        ? () => new ImkitWorker({ name: "imkit:" + session.userId })
        : undefined,
  });
  const client = new ImClient({ endpoints: [wsUrl], tokenProvider: token, transport: tabs.transport, deviceName: deviceName() });
  const store = await IdbChatStore.open({ name: "imkit-sample", encrypt: true });
  const engine = new ChatEngine(client, store, { appUserId: session.userId });

  const authed = (path: string) => fetch(path, { headers: { Authorization: `Bearer ${session.accessToken}` } }).then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))));
  const contacts = new Signal<ImProfile[]>([]);
  const toProfile = (p: { uid: string; displayName?: string; avatarUrl?: string | null }): ImProfile => ({ uid: p.uid, name: p.displayName || p.uid, ...(p.avatarUrl ? { avatarUrl: p.avatarUrl } : {}) });
  const refreshContacts = async () => {
    const r = (await authed("/api/v1/chat/contacts?status=accepted")) as { items: { uid: string; displayName: string; avatarUrl: string | null }[] };
    contacts.set(r.items.map(toProfile));
    // Single chats come from the host's contacts (the protocol lists no single chats).
    for (const c of r.items) await engine.ensureConversation(c.uid, c.displayName);
  };
  const uploader = new MediaUploader(location.origin, token);
  const host: Stack["host"] = {
    contacts,
    media: new MediaSender(engine, uploader),
    profiles: async (uids) => {
      const r = (await authed("/api/v1/chat/profiles?uids=" + encodeURIComponent(uids.join(",")))) as { items: { uid: string; displayName: string; avatarUrl: string | null }[] };
      return r.items.map(toProfile);
    },
    resolveMediaUrl: (url) => (url ? new URL(url, location.origin).toString() : undefined),
  };

  await engine.open();
  client.events.on((e) => {
    if (e.kind === "contactSync") void refreshContacts().catch(() => {});
  });
  await refreshContacts().catch(() => {});
  client.start();

  return {
    session,
    client,
    engine,
    host,
    tabs,
    refreshContacts,
    close() {
      engine.close();
      client.stop();
      tabs.close();
      store.close();
    },
  };
}
