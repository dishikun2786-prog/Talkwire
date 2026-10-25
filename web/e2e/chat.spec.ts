/**
 * The web sample against a real server (5.10 / M3): people message each other
 * live, a group works end to end, and one account on two tabs and a second
 * device receives everywhere. Every run uses fresh names, so runs don't see
 * each other's history.
 */

import { expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";

const run = Math.random().toString(36).slice(2, 8);
const user = (who: string) => `e2e_${who}_${run}`;

const contexts: BrowserContext[] = [];
test.afterAll(async () => {
  for (const c of contexts.splice(0)) await c.close();
});

async function signIn(page: Page, name: string): Promise<void> {
  await page.goto("/");
  await page.locator('input[name="name"]').fill(name);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expectSignedIn(page, name);
}

async function expectSignedIn(page: Page, name: string): Promise<void> {
  await expect(page.getByTestId("me")).toHaveText(name);
  // The banner shows while connecting or offline; it goes once logged in.
  await expect(page.locator(".imkit-banner")).toHaveCount(0);
}

/** A browser profile of its own: a separate device as far as the server is concerned. */
async function device(browser: Browser, name: string): Promise<Page> {
  const context = await browser.newContext();
  contexts.push(context);
  const page = await context.newPage();
  await signIn(page, name);
  return page;
}

async function openContact(page: Page, name: string): Promise<void> {
  const contact = page.locator(`[data-contact="${name}"]`);
  await expect(contact).toBeVisible();
  await contact.click();
  await expect(page.locator(".imkit-chat-title")).toHaveText(name);
}

async function openConversation(page: Page, title: string): Promise<void> {
  const item = conversationItem(page, title);
  await expect(item).toBeVisible();
  await item.click();
  await expect(page.locator(".imkit-chat-title")).toHaveText(title);
}

async function send(page: Page, text: string): Promise<void> {
  const box = page.getByRole("textbox", { name: "Message" });
  await box.fill(text);
  await box.press("Enter");
}

/** The conversation list's entry (contacts use the same style but carry data-contact instead). */
const conversationItem = (page: Page, title: string) => page.locator("[data-conversation]", { hasText: title });

const row = (page: Page, text: string) => page.locator(".imkit-row", { hasText: text });

/** The message is on screen and the server has given it a sequence number. */
async function expectStored(page: Page, text: string, mine: boolean): Promise<void> {
  const r = row(page, text);
  await expect(r).toHaveAttribute("data-seq", /^\d+$/);
  if (mine) await expect(r).toHaveClass(/imkit-row-mine/);
  else await expect(r).not.toHaveClass(/imkit-row-mine/);
}

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
