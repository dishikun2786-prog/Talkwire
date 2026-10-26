/**
 * Message types (5.9), named as in the Android kit: a `MessageTypeRegistry`
 * of `MessageType`s tried in order, the app's own first, then the built-in
 * `TextType`, `ImageType`, `VideoType`, `VoiceType`, `FileType`,
 * `SystemType`, then `UnknownType`. The type is chosen from `contentType`
 * (and `extra`), never from the text in `content`, which is only a fallback
 * for clients that do not know the type (C7).
 */

import { isRecalled, messageKind, type ChatMessage } from "@talkwire/client";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useProfile } from "./context.js";
import { format, type ImStrings } from "./strings.js";

/** What a message type gets from the screen it is drawn on. */
export interface MessageScope {
  isMine: boolean;
  strings: ImStrings;
  /** Turns a stored media URL into one that can be loaded. */
  resolveUrl(url: string | undefined): string | undefined;
  /** Opens an image full screen. */
  openImage(url: string): void;
}

/**
 * What a message's menu can offer, in this order. `reply`, `forward` and
 * `select` need a stored message (one with a seq); `recall`, `retry` and
 * `discard` only ever apply to own messages.
 */
export type MessageAction = "copy" | "save" | "reply" | "forward" | "select" | "recall" | "retry" | "discard";

export const MESSAGE_ACTION_ORDER: readonly MessageAction[] = ["copy", "save", "reply", "forward", "select", "recall", "retry", "discard"];

/** The message a reply quotes, carried in `extra.replyTo` of the reply (as the Android kit writes it). */
export interface ReplyRef {
  seq: number;
  from: string;
  text: string;
}

/** The quoted message of a reply; null when `message` quotes nothing. */
export function replyRefOf(message: ChatMessage): ReplyRef | null {
  const r = extraOf(message, "replyTo");
  if (!r || typeof r !== "object" || Array.isArray(r)) return null;
  const o = r as Record<string, unknown>;
  if (typeof o.seq !== "number") return null;
  return { seq: o.seq, from: typeof o.from === "string" ? o.from : "", text: typeof o.text === "string" ? o.text : "" };
}

/** The `extra` of a reply to `ref`; the quoted text is cut to keep messages small. */
export function replyExtra(ref: ReplyRef): { replyTo: ReplyRef } {
  return { replyTo: { seq: ref.seq, from: ref.from, text: Array.from(ref.text).slice(0, 120).join("") } };
}

export interface MessageType {
  /** A stable name, for tests and debugging. */
  readonly name: string;
  matches(message: ChatMessage): boolean;
  /** True: drawn inside the standard bubble. False: a card that draws its own container. */
  readonly framed?: boolean;
  render(message: ChatMessage, scope: MessageScope): ReactNode;
  /** One line for the conversation list and notifications. */
  preview(message: ChatMessage, strings: ImStrings): string;
  /** What a context menu offers; recall only ever applies to own messages. */
  actions?(message: ChatMessage): MessageAction[];
}

/** Builds a registry: `new MessageTypeRegistry.Builder().register(myType).build()`. */
export class MessageTypeRegistryBuilder {
  private readonly custom: MessageType[] = [];

  /** Adds a type before the built-in ones, so it can also take over a built-in content type. */
  register(type: MessageType): this {
    this.custom.push(type);
    return this;
  }

  build(): MessageTypeRegistry {
    return new MessageTypeRegistry([...this.custom, ...BuiltInTypes, UnknownType]);
  }
}

export class MessageTypeRegistry {
  /** Use the Builder; the list must end with a type that matches everything. */
  constructor(private readonly types: readonly MessageType[]) {}

  typeOf(message: ChatMessage): MessageType {
    return this.types.find((t) => t.matches(message)) ?? UnknownType;
  }

  preview(message: ChatMessage, strings: ImStrings): string {
    return this.typeOf(message).preview(message, strings);
  }

  static readonly Builder = MessageTypeRegistryBuilder;

  private static defaultRegistry: MessageTypeRegistry | undefined;

  /** The built-in types only. */
  static get Default(): MessageTypeRegistry {
    return (MessageTypeRegistry.defaultRegistry ??= new MessageTypeRegistryBuilder().build());
  }
}

/** Reads a field of `extra` without caring whether the sender put an object there. */
export function extraOf(message: ChatMessage, name: string): unknown {
  const e = message.extra;
  return e && typeof e === "object" && !Array.isArray(e) ? (e as Record<string, unknown>)[name] : undefined;
}

export const extraNumber = (m: ChatMessage, name: string): number => (typeof extraOf(m, name) === "number" ? (extraOf(m, name) as number) : 0);
export const extraString = (m: ChatMessage, name: string): string | undefined => (typeof extraOf(m, name) === "string" && extraOf(m, name) ? (extraOf(m, name) as string) : undefined);

/** Seconds, from the message's own field, else `extra.duration` (older senders). */
export const durationSeconds = (m: ChatMessage): number => (m.duration > 0 ? m.duration : extraNumber(m, "duration"));

/** A voice bubble grows with the clip's length (C7), between a short and a long width. */
export function voiceBubbleWidth(seconds: number): number {
  return Math.round(Math.min(220, Math.max(72, 72 + seconds * 4)));
}

/** The height a media card gets from its stated size, within bounds; a square when unknown. */
function mediaBox(width: number, height: number, max = 220): { width: number; height: number } {
  if (width <= 0 || height <= 0) return { width: 160, height: 160 };
  const scale = Math.min(max / width, max / height, 1);
  return { width: Math.max(80, Math.round(width * scale)), height: Math.max(60, Math.round(height * scale)) };
}

/** What every message offers, plus `extra`: reply, forward and select once stored, recall when sent, retry and discard when failed. */
const baseActions = (m: ChatMessage, extra: MessageAction[] = []): MessageAction[] => {
  if (m.status === "failed") return [...extra, "retry", "discard"];
  const stored: MessageAction[] = m.seq !== null ? ["reply", "forward", "select"] : [];
  return [...extra, ...stored, ...(m.status === "sent" ? (["recall"] as MessageAction[]) : [])];
};

export const TextType: MessageType = {
  name: "text",
  matches: (m) => messageKind(m.contentType, m.fileUrl) === "text",
  render: (m) => <span className="talkwire-text">{m.content}</span>,
  preview: (m) => m.content,
  actions: (m) => baseActions(m, ["copy"]),
};

export const ImageType: MessageType = {
  name: "image",
  matches: (m) => messageKind(m.contentType, m.fileUrl) === "image",
  framed: false,
  render: (m, scope) => {
    const url = scope.resolveUrl(m.fileUrl);
    const box = mediaBox(extraNumber(m, "imageWidth"), extraNumber(m, "imageHeight"));
    return url ? (
      <img className="talkwire-media" src={url} alt="" style={box} loading="lazy" onClick={() => scope.openImage(url)} />
    ) : (
      <span className="talkwire-media talkwire-media-missing" style={box} />
    );
  },
  preview: (_m, s) => s.previewImage,
  actions: (m) => baseActions(m, m.fileUrl ? ["save"] : []),
};

export const VideoType: MessageType = {
  name: "video",
  matches: (m) => messageKind(m.contentType, m.fileUrl) === "video",
  framed: false,
  render: (m, scope) => <VideoContent message={m} scope={scope} />,
  preview: (_m, s) => s.previewVideo,
  actions: (m) => baseActions(m, m.fileUrl ? ["save"] : []),
};

function VideoContent({ message, scope }: { message: ChatMessage; scope: MessageScope }) {
  const [playing, setPlaying] = useState(false);
  const url = scope.resolveUrl(message.fileUrl);
  const thumb = scope.resolveUrl(extraString(message, "thumbUrl"));
  const box = mediaBox(extraNumber(message, "thumbWidth"), extraNumber(message, "thumbHeight"));
  if (playing && url) return <video className="talkwire-media" src={url} style={box} controls autoPlay />;
  return (
    <button type="button" className="talkwire-media talkwire-video-poster" style={{ ...box, backgroundImage: thumb ? `url("${thumb}")` : undefined }} onClick={() => setPlaying(true)}>
      <span className="talkwire-play">▶</span>
      {durationSeconds(message) > 0 && <span className="talkwire-video-duration">{clock(durationSeconds(message))}</span>}
    </button>
  );
}

const clock = (s: number) => `${Math.floor(s / 60)}:${String(Math.floor(s % 60)).padStart(2, "0")}`;

export const VoiceType: MessageType = {
  name: "voice",
  matches: (m) => messageKind(m.contentType, m.fileUrl) === "voice",
  render: (m, scope) => <VoiceContent message={m} scope={scope} />,
  preview: (_m, s) => s.previewVoice,
  actions: (m) => baseActions(m, m.fileUrl ? ["save"] : []),
};

function VoiceContent({ message, scope }: { message: ChatMessage; scope: MessageScope }) {
  const seconds = Math.max(1, Math.round(durationSeconds(message)));
  const audio = useRef<HTMLAudioElement | null>(null);
  const [playing, setPlaying] = useState(false);
  useEffect(() => () => audio.current?.pause(), []);
  const toggle = () => {
    const url = scope.resolveUrl(message.fileUrl);
    if (!url) return;
    if (!audio.current) {
      audio.current = new Audio(url);
      audio.current.onended = () => setPlaying(false);
      audio.current.onpause = () => setPlaying(false);
    }
    if (playing) audio.current.pause();
    else void audio.current.play().then(() => setPlaying(true), () => setPlaying(false));
  };
  return (
    <button type="button" className={"talkwire-voice" + (playing ? " talkwire-voice-playing" : "")} style={{ width: voiceBubbleWidth(seconds) }} onClick={toggle} data-seconds={seconds}>
      <span className="talkwire-voice-icon">{playing ? "❚❚" : "▶"}</span>
      <span className="talkwire-voice-length">{format(scope.strings.voiceSeconds, seconds)}</span>
    </button>
  );
}

export const FileType: MessageType = {
  name: "file",
  matches: (m) => messageKind(m.contentType, m.fileUrl) === "file",
  render: (m, scope) => (
    <a className="talkwire-file" href={scope.resolveUrl(m.fileUrl)} target="_blank" rel="noreferrer" download={m.fileName}>
      <span className="talkwire-file-icon">📄</span>
      <span className="talkwire-file-name">{m.fileName || m.content}</span>
    </a>
  ),
  preview: (m, s) => format(s.previewFile, m.fileName ?? ""),
  actions: (m) => baseActions(m, m.fileUrl ? ["save"] : []),
};

/** A recalled message's tombstone: a centered note saying who unsent it. */
export const RecalledType: MessageType = {
  name: "recalled",
  matches: (m) => isRecalled(m),
  framed: false,
  render: (m, scope) => <RecalledNote message={m} scope={scope} />,
  preview: (_m, s) => s.recalledPreview,
  actions: () => [],
};

function RecalledNote({ message, scope }: { message: ChatMessage; scope: MessageScope }) {
  const who = useProfile(scope.isMine ? undefined : message.from);
  return <span className="talkwire-system">{scope.isMine ? scope.strings.recalledByYou : format(scope.strings.recalledBy, who?.name ?? "")}</span>;
}

export const SystemType: MessageType = {
  name: "system",
  matches: (m) => messageKind(m.contentType, m.fileUrl) === "system",
  framed: false,
  render: (m) => <span className="talkwire-system">{m.content}</span>,
  preview: (m) => m.content,
  actions: () => [],
};

/** Anything no other type claims: the fallback text of a type this app does not know. */
export const UnknownType: MessageType = {
  name: "unknown",
  matches: () => true,
  render: (m, scope) => <span className="talkwire-text talkwire-unknown">{m.content || scope.strings.unsupportedMessage}</span>,
  preview: (m, s) => m.content || s.previewUnsupported,
  actions: (m) => baseActions(m, m.content ? ["copy"] : []),
};

export const BuiltInTypes: readonly MessageType[] = [RecalledType, TextType, ImageType, VideoType, VoiceType, FileType, SystemType];
