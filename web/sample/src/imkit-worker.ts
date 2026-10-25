/// <reference lib="webworker" />
// The SharedWorker that holds the account's one WebSocket for all its tabs.
import { serveSharedWorker } from "@imkit/client";

serveSharedWorker(self as unknown as SharedWorkerGlobalScope);
