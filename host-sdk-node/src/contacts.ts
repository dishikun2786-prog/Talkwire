// The contact state machine, independent of storage and HTTP. Every change
// runs inside ContactStore.withPair, so two requests about the same pair of
// users cannot interleave their read and write.
//
//   (none) --add--> pending --accept by recipient--> accepted
//   pending --reject by recipient--> rejected --add by either--> pending
//   pending --add by recipient--> accepted          (both asked)
//   pending | accepted --block by either--> blocked (blockedBy records who)
//   any --remove by either--> (none)                except a block, which only the blocker lifts

import type { Contact, ContactStatus, ContactStore } from "./types.js";

export type ContactError =
  | "cannot_add_self"
  | "already_friends"
  | "already_requested"
  | "you_blocked"
  | "blocked_by_them"
  | "not_found"
  | "not_allowed"
  | "not_friends";

export type ContactResult<T> = { ok: true; value: T } | { ok: false; error: ContactError };

const ok = <T>(value: T): ContactResult<T> => ({ ok: true, value });
const fail = <T>(error: ContactError): ContactResult<T> => ({ ok: false, error });

/** The other user of a relationship, seen from userId. */
export function otherUser(contact: Contact, userId: string): string {
  return contact.requesterId === userId ? contact.recipientId : contact.requesterId;
}

/** userId's own remark for the other user; never the other user's remark for them. */
export function aliasFor(contact: Contact, userId: string): string | null {
  if (contact.requesterId === userId) return contact.requesterAlias;
  if (contact.recipientId === userId) return contact.recipientAlias;
  return null;
}

/** Whether userId sent or received the request. */
export function directionFor(contact: Contact, userId: string): "sent" | "received" {
  return contact.requesterId === userId ? "sent" : "received";
}

export interface ContactChange {
  contact: Contact;
  /** The state before; null when the relationship was created. */
  previous: ContactStatus | null;
}

/** me asks them to be contacts. Accepts at once when they had already asked me. */
export async function addContact(store: ContactStore, me: string, them: string): Promise<ContactResult<ContactChange>> {
  if (me === them) return fail("cannot_add_self");
  return store.withPair(me, them, async () => {
    const pair = await store.findPair(me, them);
    if (!pair) {
      const contact = await store.create({ requesterId: me, recipientId: them, status: "pending" });
      return ok({ contact, previous: null });
    }
    const previous = pair.status;
    switch (pair.status) {
      case "accepted":
        return fail("already_friends");
      case "blocked":
        return fail(pair.blockedBy === me ? "you_blocked" : "blocked_by_them");
      case "pending":
        if (pair.requesterId === me) return fail("already_requested");
        return ok({ contact: await store.update(pair.id, { status: "accepted" }), previous });
      case "rejected":
        if (pair.requesterId === me) {
          return ok({ contact: await store.update(pair.id, { status: "pending" }), previous });
        }
        // They asked and I declined; now I ask. The request becomes mine,
        // and each user keeps their own remark.
        return ok({
          contact: await store.update(pair.id, {
            requesterId: me,
            recipientId: them,
            status: "pending",
            requesterAlias: pair.recipientAlias,
            recipientAlias: pair.requesterAlias,
          }),
          previous,
        });
    }
  });
}

async function withContact<T>(
  store: ContactStore,
  me: string,
  id: string,
  fn: (contact: Contact) => Promise<ContactResult<T>>,
): Promise<ContactResult<T>> {
  const first = await store.get(id);
  if (!first || (first.requesterId !== me && first.recipientId !== me)) return fail("not_found");
  return store.withPair(first.requesterId, first.recipientId, async () => {
    // Read again under the pair lock: the row may have changed or gone.
    const contact = await store.get(id);
    if (!contact || (contact.requesterId !== me && contact.recipientId !== me)) return fail("not_found");
    return fn(contact);
  });
}

/** me accepts, rejects or blocks. */
export async function respondToContact(
  store: ContactStore,
  me: string,
  id: string,
  status: "accepted" | "rejected" | "blocked",
): Promise<ContactResult<ContactChange>> {
  return withContact(store, me, id, async (contact) => {
    if (contact.status === status && (status !== "blocked" || contact.blockedBy === me)) {
      return ok({ contact, previous: contact.status });
    }
    const previous = contact.status;
    if (status === "blocked") {
      if (contact.status !== "pending" && contact.status !== "accepted") return fail("not_allowed");
      return ok({ contact: await store.update(id, { status, blockedBy: me }), previous });
    }
    if (contact.status !== "pending" || contact.recipientId !== me) return fail("not_allowed");
    return ok({ contact: await store.update(id, { status }), previous });
  });
}

/** me sets or clears (empty or null) their remark for the other user. */
export async function setAlias(store: ContactStore, me: string, id: string, alias: string | null): Promise<ContactResult<Contact>> {
  const value = alias?.trim() ? alias.trim() : null;
  return withContact(store, me, id, async (contact) => {
    if (contact.status !== "accepted") return fail("not_friends");
    const patch = contact.requesterId === me ? { requesterAlias: value } : { recipientAlias: value };
    return ok(await store.update(id, patch));
  });
}

/** me ends the relationship. A block can only be lifted by the user who blocked. */
export async function removeContact(store: ContactStore, me: string, id: string): Promise<ContactResult<Contact>> {
  return withContact(store, me, id, async (contact) => {
    if (contact.status === "blocked" && contact.blockedBy !== me) return fail("not_allowed");
    await store.delete(id);
    return ok(contact);
  });
}
