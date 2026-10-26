/**
 * What the end-to-end specs share: fresh user names per run, a browser
 * profile per device, and the sample's chat controls.
 */

import { expect, type Browser, type BrowserContext, type Page } from "@playwright/test";

export const run = Math.random().toString(36).slice(2, 8);
export const user = (who: string) => `e2e_${who}_${run}`;

const contexts: BrowserContext[] = [];

/** Closes every device opened so far; each spec calls it in its own `test.afterAll`. */
export async function closeDevices(): Promise<void> {
  for (const c of contexts.splice(0)) await c.close();
}

export async function signIn(page: Page, name: string): Promise<void> {
  await page.goto("/");
  await page.locator('input[name="name"]').fill(name);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expectSignedIn(page, name);
}

export async function expectSignedIn(page: Page, name: string): Promise<void> {
  await expect(page.getByTestId("me")).toHaveText(name);
  // The banner shows while connecting or offline; it goes once logged in.
  await expect(page.locator(".talkwire-banner")).toHaveCount(0);
}

/** A browser profile of its own: a separate device as far as the server is concerned. */
export async function device(browser: Browser, name: string): Promise<Page> {
  const context = await browser.newContext();
  contexts.push(context);
  const page = await context.newPage();
  await signIn(page, name);
  return page;
}

/** A browser profile of its own, signed in as nobody (e.g. for the admin console). */
export async function blankPage(browser: Browser): Promise<Page> {
  const context = await browser.newContext();
  contexts.push(context);
  return context.newPage();
}

export async function openContact(page: Page, name: string): Promise<void> {
  const contact = contactItem(page, name);
  await expect(contact).toBeVisible();
  await contact.click();
  await expect(page.locator(".talkwire-chat-title")).toHaveText(name);
}

export async function openConversation(page: Page, title: string): Promise<void> {
  const item = conversationItem(page, title);
  await expect(item).toBeVisible();
  await item.click();
  await expect(page.locator(".talkwire-chat-title")).toHaveText(title);
}

export async function send(page: Page, text: string): Promise<void> {
  const box = page.getByRole("textbox", { name: "Message" });
  await box.fill(text);
  await box.press("Enter");
}

/** A contact in the sample's contact list (data-contact is the uid; the name is the text). */
export const contactItem = (page: Page, name: string) => page.locator("[data-contact]", { hasText: name });

/** The conversation list's entry (contacts use the same style but carry data-contact instead). */
export const conversationItem = (page: Page, title: string) => page.locator("[data-conversation]", { hasText: title });

export const row = (page: Page, text: string) => page.locator(".talkwire-row", { hasText: text });

/** The message is on screen and the server has given it a sequence number. */
export async function expectStored(page: Page, text: string, mine: boolean): Promise<void> {
  const r = row(page, text);
  await expect(r).toHaveAttribute("data-seq", /^\d+$/);
  if (mine) await expect(r).toHaveClass(/talkwire-row-mine/);
  else await expect(r).not.toHaveClass(/talkwire-row-mine/);
}
