// @talkwire/host-node: the host side of Talkwire for Node and Fastify.
//
//   talkwireHostPlugin   the endpoints the IM server calls (host.openapi.yaml)
//   talkwireAppRoutes    profiles and contacts for the chat app
//   talkwireMediaRoutes  chat media upload, serving and deletion
//   talkwireAdminRoutes  chat records for administrators
//   ImClient          the endpoints the IM server exposes to the host
//
// Storage goes through the interfaces in types.ts: memory stores here, Prisma
// stores in "@talkwire/host-node/prisma", an S3 blob store in "@talkwire/host-node/s3".

export * from "./types.js";
export { imUid, isUid } from "./uid.js";
export { signInternalToken, verifyInternalToken, bearerToken } from "./token.js";
export { talkwireHostPlugin, type HostPluginOptions, type CallHandlers, type GroupAiHandler, type GroupAiRequest } from "./host-plugin.js";
export { talkwireAppRoutes, defaultHandle, type AppRoutesOptions, type AppEvent } from "./app-routes.js";
export { talkwireAdminRoutes, clearConfirmation, type AdminRoutesOptions, type AdminIdentity } from "./admin-routes.js";
export {
  talkwireMediaRoutes,
  mediaDeleter,
  mediaName,
  ownerTag,
  DEFAULT_MEDIA_TYPES,
  type MediaOptions,
} from "./media/media.js";
export type { BlobStore, StoredBlob } from "./media/types.js";
export { LocalDiskBlobStore } from "./media/local.js";
export {
  ImClient,
  ImClientError,
  type ImClientOptions,
  type AdminTopic,
  type AdminMessage,
  type AdminMessagesQuery,
  type ClearResult,
  type BroadcastTarget,
} from "./im-client.js";
export { issueAddToken, checkAddToken, issueContactQr, checkContactQr, type AddTokenMode } from "./add-token.js";
export {
  addContact,
  respondToContact,
  setAlias,
  removeContact,
  otherUser,
  aliasFor,
  directionFor,
  type ContactError,
  type ContactResult,
  type ContactChange,
} from "./contacts.js";
export { MemoryProfileStore, MemoryContactStore, MemoryAuditStore, MemoryCallRecordStore, KeyedMutex, staticConfig } from "./memory.js";
export { recordingCalls, type RecordingCallsOptions } from "./call-records.js";
