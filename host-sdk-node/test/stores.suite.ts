// One set of cases for every implementation of the storage interfaces.

import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { addContact, aliasFor, removeContact, respondToContact, setAlias } from "../src/contacts.js";
import {
  DuplicateKeyError,
  HandleTakenError,
  PairExistsError,
  type AuditStore,
  type ContactStore,
  type ProfileStore,
} from "../src/types.js";
import { imUid } from "../src/uid.js";

export interface Stores {
  profiles: ProfileStore;
  contacts: ContactStore;
  audit: AuditStore;
}

const tick = () => new Promise((r) => setTimeout(r, 5));

export function storeSuite(name: string, make: () => Promise<Stores>, options: { skip?: string } = {}) {
  describe(`${name} stores`, { skip: options.skip }, () => {
    test("profiles: create, look up, handles unique regardless of case", async () => {
      const { profiles } = await make();
      const alice = await profiles.create({ userId: "u-alice", handle: "Alice01", displayName: "Alice", avatarUrl: null });
      assert.equal(alice.uid, imUid("u-alice"));
      assert.equal(alice.handleChangedAt, null);
      assert.equal((await profiles.get("u-alice"))?.displayName, "Alice");
      assert.equal(await profiles.get("nobody"), null);
      assert.equal((await profiles.findByHandle("alice01"))?.userId, "u-alice");
      assert.equal((await profiles.findByHandle("ALICE01"))?.handle, "Alice01");
      assert.equal((await profiles.findByUid(imUid("u-alice")))?.userId, "u-alice");
      await assert.rejects(profiles.create({ userId: "u-bob", handle: "aLiCe01", displayName: "", avatarUrl: null }), HandleTakenError);
      const bob = await profiles.create({ userId: "u-bob", handle: "bob1", displayName: "Bob", avatarUrl: "https://x/b.png" });
      assert.deepEqual((await profiles.getMany(["u-alice", "u-bob", "nobody"])).map((p) => p.userId).sort(), ["u-alice", "u-bob"]);
      assert.deepEqual((await profiles.findManyByUid([bob.uid, "uid_nobodynobod"])).map((p) => p.userId), ["u-bob"]);
      assert.deepEqual(await profiles.getMany([]), []);
      await assert.rejects(profiles.update("u-bob", { handle: "ALICE01" }), HandleTakenError);
      await tick();
      const changed = await profiles.update("u-bob", { handle: "Robert", handleChangedAt: new Date(), avatarUrl: null });
      assert.equal(changed.handle, "Robert");
      assert.equal(changed.avatarUrl, null);
      assert.ok(changed.updatedAt > bob.updatedAt);
      assert.equal(await profiles.findByHandle("bob1"), null);
      assert.equal((await profiles.findByHandle("robert"))?.userId, "u-bob");
    });

    test("contacts: one per pair, listing by direction, status and time", async () => {
      const { contacts } = await make();
      const ab = await contacts.create({ requesterId: "a", recipientId: "b", status: "pending" });
      assert.equal((await contacts.findPair("b", "a"))?.id, ab.id);
      await assert.rejects(contacts.create({ requesterId: "b", recipientId: "a", status: "pending" }), PairExistsError);
      await tick();
      const ac = await contacts.create({ requesterId: "c", recipientId: "a", status: "pending" });
      assert.deepEqual((await contacts.list("a", { status: "pending", direction: "all" })).map((c) => c.id), [ac.id, ab.id]);
      assert.deepEqual((await contacts.list("a", { status: "pending", direction: "sent" })).map((c) => c.id), [ab.id]);
      assert.deepEqual((await contacts.list("a", { status: "pending", direction: "received" })).map((c) => c.id), [ac.id]);
      assert.deepEqual(await contacts.list("a", { status: "accepted", direction: "all" }), []);
      assert.deepEqual((await contacts.list("a", { status: "pending", direction: "all", updatedAfter: ab.updatedAt })).map((c) => c.id), [ac.id]);
      assert.deepEqual((await contacts.list("a", { status: "pending", direction: "all", updatedUntil: ab.updatedAt })).map((c) => c.id), [ab.id]);
      const blocked = await contacts.update(ab.id, { status: "blocked", blockedBy: "b" });
      assert.equal(blocked.blockedBy, "b");
      assert.equal((await contacts.update(ab.id, { status: "accepted" })).blockedBy, null);
      assert.equal(await contacts.get("not-an-id"), null);
      assert.equal(await contacts.get("00000000-0000-4000-8000-000000000000"), null);
      await contacts.delete(ab.id);
      await contacts.delete(ab.id);
      assert.equal(await contacts.findPair("a", "b"), null);
    });

    test("contact state machine: request, accept, remarks stay with their author", async () => {
      const { contacts } = await make();
      const req = await addContact(contacts, "alice", "bob");
      assert.ok(req.ok && req.value.contact.status === "pending" && req.value.previous === null);
      const id = req.value.contact.id;
      assert.deepEqual(await addContact(contacts, "alice", "bob"), { ok: false, error: "already_requested" });
      assert.deepEqual(await respondToContact(contacts, "alice", id, "accepted"), { ok: false, error: "not_allowed" });
      assert.deepEqual(await setAlias(contacts, "alice", id, "Bobby"), { ok: false, error: "not_friends" });
      const accepted = await respondToContact(contacts, "bob", id, "accepted");
      assert.ok(accepted.ok && accepted.value.contact.status === "accepted" && accepted.value.previous === "pending");
      assert.deepEqual(await addContact(contacts, "bob", "alice"), { ok: false, error: "already_friends" });

      // Two accounts, two remarks: each sees only their own.
      assert.ok((await setAlias(contacts, "alice", id, "  Bobby ")).ok);
      assert.ok((await setAlias(contacts, "bob", id, "Ally")).ok);
      const row = (await contacts.get(id))!;
      assert.equal(aliasFor(row, "alice"), "Bobby");
      assert.equal(aliasFor(row, "bob"), "Ally");
      assert.equal(aliasFor(row, "carol"), null);
      assert.ok((await setAlias(contacts, "alice", id, "")).ok);
      assert.equal(aliasFor((await contacts.get(id))!, "alice"), null);
      assert.equal(aliasFor((await contacts.get(id))!, "bob"), "Ally");
      assert.deepEqual(await setAlias(contacts, "carol", id, "x"), { ok: false, error: "not_found" });
    });

    test("contact state machine: rejection, asking back, blocks", async () => {
      const { contacts } = await make();
      const first = await addContact(contacts, "alice", "bob");
      assert.ok(first.ok);
      const id = first.value.contact.id;
      assert.ok((await respondToContact(contacts, "bob", id, "rejected")).ok);
      // Bob asks back: the request is now his; nobody's remark moves to the other.
      const back = await addContact(contacts, "bob", "alice");
      assert.ok(back.ok && back.value.contact.requesterId === "bob" && back.value.previous === "rejected");
      // Alice asks again while Bob's request is pending: that accepts it.
      const both = await addContact(contacts, "alice", "bob");
      assert.ok(both.ok && both.value.contact.status === "accepted");
      const block = await respondToContact(contacts, "alice", id, "blocked");
      assert.ok(block.ok && block.value.contact.blockedBy === "alice");
      assert.deepEqual(await addContact(contacts, "bob", "alice"), { ok: false, error: "blocked_by_them" });
      assert.deepEqual(await addContact(contacts, "alice", "bob"), { ok: false, error: "you_blocked" });
      assert.deepEqual(await removeContact(contacts, "bob", id), { ok: false, error: "not_allowed" });
      assert.ok((await removeContact(contacts, "alice", id)).ok);
      assert.equal(await contacts.findPair("alice", "bob"), null);
    });

    test("contact state machine: rejection keeps each side's remark on its side", async () => {
      const { contacts } = await make();
      const req = await addContact(contacts, "p", "q");
      assert.ok(req.ok);
      const id = req.value.contact.id;
      assert.ok((await respondToContact(contacts, "q", id, "accepted")).ok);
      assert.ok((await setAlias(contacts, "p", id, "from-p")).ok);
      assert.ok((await setAlias(contacts, "q", id, "from-q")).ok);
      // A rejected relationship asked again by the other side swaps roles.
      await contacts.update(id, { status: "rejected" });
      const back = await addContact(contacts, "q", "p");
      assert.ok(back.ok && back.value.contact.requesterId === "q");
      assert.equal(aliasFor(back.value.contact, "p"), "from-p");
      assert.equal(aliasFor(back.value.contact, "q"), "from-q");
    });

    test("withPair: both users asking at once ends with one accepted relationship", async () => {
      const { contacts } = await make();
      const results = await Promise.all([addContact(contacts, "x1", "y1"), addContact(contacts, "y1", "x1")]);
      assert.ok(results.every((r) => r.ok), JSON.stringify(results));
      const pair = (await contacts.findPair("x1", "y1"))!;
      assert.equal(pair.status, "accepted");
      const many = await Promise.all(Array.from({ length: 6 }, (_, i) => addContact(contacts, i % 2 ? "x2" : "y2", i % 2 ? "y2" : "x2")));
      assert.equal(many.filter((r) => r.ok).length >= 2, true);
      assert.equal((await contacts.findPair("x2", "y2"))?.status, "accepted");
    });

    test("audit: metadata, idempotency keys, outcome", async () => {
      const { audit } = await make();
      const e = await audit.create({ adminId: "admin-1", action: "im.clear_topic", status: "pending", topic: "p2p_x", reason: "user asked", idempotencyKey: "key-12345678" });
      assert.equal(e.status, "pending");
      assert.equal(e.completedAt, null);
      await assert.rejects(audit.create({ adminId: "admin-2", action: "x", status: "pending", idempotencyKey: "key-12345678" }), DuplicateKeyError);
      const done = await audit.finish(e.id, "success", { deletedMessages: 3 });
      assert.equal(done.status, "success");
      assert.ok(done.completedAt);
      assert.deepEqual(done.details, { deletedMessages: 3 });
      assert.deepEqual((await audit.finish(e.id, "success", null)).details, { deletedMessages: 3 });
      assert.equal((await audit.findByIdempotencyKey("key-12345678"))?.id, e.id);
      assert.equal(await audit.findByIdempotencyKey("other-key-1"), null);
      assert.equal((await audit.get(e.id))?.reason, "user asked");
      assert.equal(await audit.get("nope"), null);
      const bare = await audit.create({ adminId: "admin-1", action: "y", status: "pending" });
      assert.equal(bare.idempotencyKey, null);
      assert.equal(bare.details, null);
    });
  });
}
