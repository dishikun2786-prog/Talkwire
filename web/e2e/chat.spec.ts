/**
 * The web sample against a real server (5.10 / M3): people message each other
 * live, a group works end to end, and one account on two tabs and a second
 * device receives everywhere. Every run uses fresh names, so runs don't see
 * each other's history.
 */

import { expect, test } from "@playwright/test";
import { closeDevices, conversationItem, device, expectSignedIn, expectStored, openContact, openConversation, run, send, user } from "./helpers.js";

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

  await expect(c.locator(`[data-contact="${dave}"]`)).toBeVisible();
  await c.getByRole("button", { name: "New group" }).click();
  const dialog = c.getByRole("dialog", { name: "New group" });
  await dialog.getByLabel("Group name").fill(group);
  await dialog.locator(".sample-pick", { hasText: dave }).getByRole("checkbox").check();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(c.locator(".imkit-chat-title")).toHaveText(group);
  await send(c, "hello group");
  await expectStored(c, "hello group", true);

  await openConversation(d, group);
  await expectStored(d, "hello group", false);
  await send(d, "hi carol");
  await expectStored(c, "hi carol", false);
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
