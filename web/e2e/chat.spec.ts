/**
 * The web sample against a real server (5.10 / M3): people message each other
 * live, a group works end to end, and one account on two tabs and a second
 * device receives everywhere. Every run uses fresh names, so runs don't see
 * each other's history.
 */

import { expect, test } from "@playwright/test";
import { closeDevices, contactItem, conversationItem, device, expectSignedIn, expectStored, openContact, openConversation, row, run, send, user } from "./helpers.js";

test.afterAll(closeDevices);

test("two people message each other in real time", async ({ browser }) => {
  const alice = user("alice");
  const bob = user("bob");
  const a = await device(browser, alice);
  const b = await device(browser, bob);

  // Bob signing in befriended Alice; her contacts update without a reload.
  await openContact(a, bob);
  await send(a, "hi bob");
  await expectStored(a, "hi bob", true);

  await expect(conversationItem(b, alice)).toContainText("hi bob");
  await openConversation(b, alice);
  await expectStored(b, "hi bob", false);

  await send(b, "hi alice");
  await expectStored(b, "hi alice", true);
  await expectStored(a, "hi alice", false);
});

test("a group: create, invite, talk", async ({ browser }) => {
  const carol = user("carol");
  const dave = user("dave");
  const c = await device(browser, carol);
  const d = await device(browser, dave);
  const group = "e2e group " + run;

  await expect(contactItem(c, dave)).toBeVisible();
  await c.getByRole("button", { name: "New group" }).click();
  const dialog = c.getByRole("dialog", { name: "New group" });
  await dialog.getByLabel("Group name").fill(group);
  await dialog.locator(".imkit-info-row", { hasText: dave }).getByRole("checkbox").check();
  await dialog.getByRole("button", { name: /^Create/ }).click();
  await expect(c.locator(".imkit-chat-title")).toHaveText(group);
  await send(c, "hello group");
  await expectStored(c, "hello group", true);

  await openConversation(d, group);
  await expectStored(d, "hello group", false);
  // Dave has it open, so it is read: Carol sees who read it.
  await expect(c.locator(".imkit-row", { hasText: "hello group" }).locator(".imkit-readers")).toHaveText(/Read by 1$/);
  await send(d, "hi carol");
  await expectStored(c, "hi carol", false);

  // The owner makes Dave an admin; as an admin he may rename the group, and Carol sees the new name.
  await c.getByRole("button", { name: "Group info" }).click();
  const info = c.locator(".imkit-info");
  await expect(info.getByText("Members (2)")).toBeVisible();
  await info.locator(".imkit-info-row", { hasText: dave }).click();
  await c.getByRole("menuitem", { name: "Make admin" }).click();
  await expect(info.locator(".imkit-info-row", { hasText: dave }).locator(".imkit-role")).toHaveText("Admin");

  await d.getByRole("button", { name: "Group info" }).click();
  const dInfo = d.locator(".imkit-info");
  await expect(dInfo.locator(".imkit-info-row", { hasText: "You" }).locator(".imkit-role")).toHaveText("Admin");
  await expect(dInfo.getByRole("button", { name: "Leave group" })).toBeVisible();
  await dInfo.getByRole("button", { name: "Rename group" }).click();
  const rename = d.getByRole("dialog", { name: "Rename group" });
  await rename.getByLabel("Group name").fill(group + " renamed");
  await rename.getByRole("button", { name: "Save" }).click();
  await expect(info.locator(".imkit-info-title")).toHaveText(group + " renamed");
});

test("one account on two tabs and a second device receives everywhere", async ({ browser }) => {
  const eve = user("eve");
  const frank = user("frank");
  const tab1 = await device(browser, eve);
  // A second tab of the same profile signs in from the saved session and shares the connection.
  const tab2 = await tab1.context().newPage();
  await tab2.goto("/");
  await expectSignedIn(tab2, eve);
  await expect(tab1.getByTestId("link")).toHaveText("worker");
  await expect(tab2.getByTestId("link")).toHaveText("worker");

  const f = await device(browser, frank);
  await openContact(f, eve);
  await send(f, "to every eve");
  await expectStored(f, "to every eve", true);

  for (const t of [tab1, tab2]) {
    await openConversation(t, frank);
    await expectStored(t, "to every eve", false);
  }

  // What one tab sends shows in the other, as sent from this account.
  await send(tab1, "from tab one");
  await expectStored(tab1, "from tab one", true);
  await expectStored(tab2, "from tab one", true);
  await expectStored(f, "from tab one", false);

  // A second device for the same account (its own profile, its own device name).
  const phone = await device(browser, eve);
  await openContact(phone, frank);
  await expectStored(phone, "from tab one", true);
  await send(f, "both devices?");
  for (const t of [tab1, tab2, phone]) await expectStored(t, "both devices?", false);
});

test("reply with a quote, forward to another chat, forward several at once", async ({ browser }) => {
  const xan = user("xan");
  const yui = user("yui");
  const zed = user("zed");
  const x = await device(browser, xan);
  const y = await device(browser, yui);
  const z = await device(browser, zed);

  await openContact(x, yui);
  await send(x, "the news");
  await send(x, "more news");
  await expectStored(x, "more news", true);

  // Yui replies to the first one; both sides show the quote.
  await openConversation(y, xan);
  await expectStored(y, "the news", false);
  await y.locator(".imkit-row", { hasText: "the news" }).locator(".imkit-bubble").click({ button: "right" });
  await y.getByRole("menuitem", { name: "Reply" }).click();
  await expect(y.locator(".imkit-reply-bar")).toContainText("Reply to " + xan);
  await send(y, "thanks");
  await expectStored(y, "thanks", true);
  // Xan sees her own message quoted as "You".
  for (const [p, who] of [[x, "You"], [y, xan]] as const) {
    const quote = p.locator(".imkit-row", { hasText: "thanks" }).locator(".imkit-quote");
    await expect(quote.locator(".imkit-quote-name")).toHaveText(who);
    await expect(quote.locator(".imkit-quote-text")).toHaveText("the news");
  }

  // Yui forwards one message to Zed.
  await y.locator(".imkit-row", { hasText: "the news" }).first().locator(".imkit-bubble").click({ button: "right" });
  await y.getByRole("menuitem", { name: "Forward" }).click();
  const dialog = y.getByRole("dialog", { name: "Forward to" });
  await dialog.locator(".imkit-info-row", { hasText: zed }).getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Send (1)" }).click();
  await expect(y.getByRole("alert")).toHaveText("Forwarded");
  await openConversation(z, yui);
  const forwarded = z.locator(".imkit-row", { hasText: "the news" });
  await expectStored(z, "the news", false);
  await expect(forwarded.locator(".imkit-forwarded")).toHaveText("Forwarded");

  // Two at once, bundled into Zed's chat in their order.
  await y.locator(".imkit-row", { hasText: "more news" }).locator(".imkit-bubble").click({ button: "right" });
  await y.getByRole("menuitem", { name: "Select" }).click();
  await y.locator(".imkit-row", { hasText: "thanks" }).locator(".imkit-bubble").click();
  await expect(y.locator(".imkit-select-bar")).toContainText("2 selected");
  await y.locator(".imkit-select-bar").getByRole("button", { name: "Forward" }).click();
  await y.getByRole("dialog", { name: "Forward to" }).locator(".imkit-info-row", { hasText: zed }).click();
  await expect(y.getByRole("alert")).toHaveText("Forwarded");
  await expectStored(z, "more news", false);
  await expectStored(z, "thanks", false);
  const order = await z.locator(".imkit-row").evaluateAll((rows) => rows.map((r) => r.textContent ?? ""));
  const at = (t: string) => order.findIndex((r) => r.includes(t));
  expect(at("more news")).toBeLessThan(at("thanks"));
});

test("unsend leaves a note on both sides; search finds a message and opens its chat", async ({ browser }) => {
  const erin = user("erin");
  const frank = user("frank");
  const e = await device(browser, erin);
  const f = await device(browser, frank);
  await openContact(e, frank);
  await send(e, "the meeting moved to Friday");
  await expectStored(e, "the meeting moved to Friday", true);
  await send(e, "oops wrong chat");
  await expectStored(e, "oops wrong chat", true);
  await openConversation(f, erin);
  await expectStored(f, "oops wrong chat", false);

  await row(e, "oops wrong chat").locator(".imkit-bubble").click({ button: "right" });
  await e.getByRole("menuitem", { name: "Unsend" }).click();
  await expect(e.getByText("You unsent a message")).toBeVisible();
  await expect(f.getByText(`${erin} unsent a message`)).toBeVisible();
  await expect(row(f, "oops wrong chat")).toHaveCount(0);
  await expect(conversationItem(f, erin)).toContainText("Message unsent");

  await f.getByRole("searchbox", { name: "Search chats and messages" }).fill("friday");
  const hit = f.locator("[data-hit]", { hasText: "Friday" });
  await expect(hit).toBeVisible();
  await expect(hit.locator("mark")).toHaveText("Friday");
  await expect(f.locator("[data-hit]", { hasText: "oops" }), "an unsent message is not found").toHaveCount(0);
  await hit.click();
  await expect(f.locator(".imkit-chat-title")).toHaveText(erin);
});
