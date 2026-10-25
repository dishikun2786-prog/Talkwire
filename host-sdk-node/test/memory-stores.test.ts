import { MemoryAuditStore, MemoryCallRecordStore, MemoryContactStore, MemoryProfileStore } from "../src/memory.js";
import { storeSuite } from "./stores.suite.js";

storeSuite("memory", async () => ({
  profiles: new MemoryProfileStore(),
  contacts: new MemoryContactStore(),
  audit: new MemoryAuditStore(),
  calls: new MemoryCallRecordStore(),
}));
