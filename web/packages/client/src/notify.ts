/**
 * Notifications in the browser (5.6). The rules, the web counterpart of C6:
 *
 * - nothing for a muted conversation, my own messages or system messages;
 * - nothing when the page is visible and showing that very conversation;
 * - the page is visible but elsewhere: an in-page banner (the app draws it)
 *   and the sound;
 * - the page is hidden: a system notification (Notification API) per
 *   conversation, replacing the previous one, and the sound.
 *
 * Offline delivery uses Web Push: the server sends `{title, body, locKey,
 * data}` (see server/push), the app's service worker shows it with
 * `showPushNotification`.
 */

import type { ImClient } from "./client.js";
import type { ChatEngine, ChatEvent } from "./engine.js";
import { messageKind, type ChatMessage, type ConversationId, type ConversationSummary } from "./model.js";
import type { Unsubscribe } from "./util.js";

export type NotifyDecision = "none" | "banner" | "system";

export interface NotifyContext {
  pageVisible: boolean;
  /** The conversation on screen, if any. */
  openConversation: ConversationId | null;
  me: string;
}

export function notifyDecision(message: ChatMessage, conversation: ConversationSummary | undefined, ctx: NotifyContext): NotifyDecision {
  if (message.from === ctx.me || message.status !== "received") return "none";
  if (messageKind(message.contentType, message.fileUrl) === "system") return "none";
  if (conversation?.muted) return "none";
  if (ctx.pageVisible) return ctx.openConversation === message.conversation ? "none" : "banner";
  return "system";
}

export interface BrowserNotifierOptions {
  /** Title and body of a notification; `preview` is what the message type shows in lists. */
  render(message: ChatMessage, conversation: ConversationSummary | undefined, groupName?: string): { title: string; body: string; icon?: string };
  /** The conversation on screen. */
  openConversation(): ConversationId | null;
  /** In-page banner while the page is visible. */
  onBanner?(message: ChatMessage, rendered: { title: string; body: string; icon?: string }): void;
  /** A system notification was clicked. */
  onOpen?(conversation: ConversationId): void;
  /** Plays the message sound; called for banners and system notifications. */
  playSound?(): void;
  pageVisible?(): boolean;
}

/** Shows notifications for messages arriving while the app is open. */
export class BrowserNotifier {
  private off: Unsubscribe | null = null;

  constructor(
    private readonly engine: ChatEngine,
    private readonly options: BrowserNotifierOptions,
  ) {}

  /** Asks for permission; call from a user gesture. */
  static async requestPermission(): Promise<NotificationPermission | "unsupported"> {
    if (typeof Notification === "undefined") return "unsupported";
    if (Notification.permission !== "default") return Notification.permission;
    return Notification.requestPermission();
  }

  start(): void {
    this.off ??= this.engine.events.on((e) => this.onEvent(e));
  }

  stop(): void {
    this.off?.();
    this.off = null;
  }

  private onEvent(e: ChatEvent): void {
    if (e.kind !== "messageArrived") return;
    const conversation = this.engine.conversation(e.message.conversation);
    const visible = this.options.pageVisible?.() ?? (typeof document === "undefined" || document.visibilityState === "visible");
    const decision = notifyDecision(e.message, conversation, { pageVisible: visible, openConversation: this.options.openConversation(), me: this.engine.me });
    if (decision === "none") return;
    const rendered = this.options.render(e.message, conversation, e.groupName);
    this.options.playSound?.();
    if (decision === "banner") {
      this.options.onBanner?.(e.message, rendered);
      return;
    }
    if (typeof Notification === "undefined" || Notification.permission !== "granted") return;
    const opts: NotificationOptions = { body: rendered.body, tag: "imkit:" + e.message.conversation, data: { conversation: e.message.conversation } };
    if (rendered.icon) opts.icon = rendered.icon;
    const n = new Notification(rendered.title, opts);
    n.onclick = () => {
      n.close();
      globalThis.focus?.();
      this.options.onOpen?.(e.message.conversation);
    };
  }
}

// ------------------------------------------------------------------ Web Push

function urlBase64ToBytes(key: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (key.length % 4)) % 4);
  const raw = atob((key + pad).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

/** Subscribes the service worker to Web Push and returns the token to register: the subscription's JSON. */
export async function subscribeWebPush(registration: ServiceWorkerRegistration, vapidKey: string): Promise<string> {
  const existing = await registration.pushManager.getSubscription();
  const sub = existing ?? (await registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToBytes(vapidKey) }));
  return JSON.stringify(sub.toJSON());
}

/**
 * Registers Web Push after every login when the server offers it (the login
 * reply's `vapidKey`). Returns a function that unregisters the token, for
 * signing out.
 */
export function enableWebPush(client: ImClient, registration: ServiceWorkerRegistration): () => Promise<void> {
  let token: string | null = null;
  const register = async () => {
    const key = client.session?.vapidKey;
    if (!key || typeof Notification === "undefined" || Notification.permission !== "granted") return;
    try {
      token = await subscribeWebPush(registration, key);
      client.registerPushToken(token, "webpush");
    } catch {
      // Push is a convenience: without it the app still works while open.
    }
  };
  const off = client.events.on((e) => {
    if (e.kind === "connected") void register();
  });
  if (client.isConnected) void register();
  return async () => {
    off();
    if (token) client.unregisterPushToken(token, "webpush");
    const sub = await registration.pushManager.getSubscription();
    await sub?.unsubscribe();
  };
}

/** The payload the server's Web Push sender encrypts (server/push). */
export interface PushPayload {
  title?: string;
  body?: string;
  locKey?: string;
  data?: Record<string, string>;
}

/**
 * For the app's service worker: shows a pushed notification. `localize`
 * turns a `locKey` (an end-to-end encrypted message has no text) into the
 * app's own words. Notifications of one conversation replace each other.
 */
export function showPushNotification(
  registration: ServiceWorkerRegistration,
  payload: PushPayload,
  options: { appName: string; localize?: (locKey: string) => string; icon?: string },
): Promise<void> {
  const data = payload.data ?? {};
  const title = payload.title || options.appName;
  const body = payload.locKey ? (options.localize?.(payload.locKey) ?? "") : (payload.body ?? "");
  const n: NotificationOptions = { body, data, tag: data.topic ? "imkit:" + data.topic : "imkit" };
  if (options.icon) n.icon = options.icon;
  return registration.showNotification(title, n);
}
