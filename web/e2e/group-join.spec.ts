/**
 * Scan-to-join (7.6) in the web sample against a real server: a member shares
 * the group's link, someone joins with it and talks; with approval switched
 * on, the next person asks, a manager approves, and the applicant is told and
 * finds the group in their list.
 */

import { expect, test, type Page } from "@playwright/test";
import { closeDevices, contactItem, conversationItem, device, expectStored, openConversation, run, send, user } from "./helpers.js";

test.afterAll(closeDevices);

async function createGroup(page: Page, name: string, member: string): Promise<void> {
  await expect(contactItem(page, member)).toBeVisible();
  await page.getByRole("button", { name: "New group" }).click();
  const dialog = page.getByRole("dialog", { name: "New group" });
  await dialog.getByLabel("Group name").fill(name);
  await dialog.locator(".imkit-info-row", { hasText: member }).getByRole("checkbox").check();
  await dialog.getByRole("button", { name: /^Create/ }).click();
  await expect(page.locator(".imkit-chat-title")).toHaveText(name);
}

test("join a group by its link, then with approval", async ({ browser }) => {
  const owner = user("gj_owner");
  const friend = user("gj_friend");
  const group = "e2e scan " + run;
  const o = await device(browser, owner);
  await device(browser, friend);
  await createGroup(o, group, friend);

  // The owner shares the group's link from the group's info page.
  await o.getByRole("button", { name: "Group info" }).click();
  const panel = o.locator(".imkit-info");
  const link = panel.locator("[data-qr-link]");
  await expect(link).toContainText("#/join/");
  const url = (await link.textContent())!.trim();
  await panel.getByRole("button", { name: "Back" }).click();

  // Someone opens it, joins at once and talks.
  const joiner = user("gj_joiner");
  const j = await device(browser, joiner);
  await j.goto(url);
  await expect(j.getByRole("heading", { name: group })).toBeVisible();
  await expect(j.getByText("2 members")).toBeVisible();
  await j.getByRole("button", { name: "Join group" }).click();
  await j.getByRole("button", { name: "Open chat" }).click();
  await expect(j.locator(".imkit-chat-title")).toHaveText(group);
  await send(j, "found you by the link");
  await expectStored(j, "found you by the link", true);
  await expectStored(o, "found you by the link", false);

  // With approval on, the next person asks and waits.
  await o.getByRole("button", { name: "Group info" }).click();
  await panel.getByLabel("Admins approve people who join by QR code").check();
  await expect(panel.getByLabel("Admins approve people who join by QR code")).toBeChecked();
  const asker = user("gj_asker");
  const a = await device(browser, asker);
  await a.goto(url);
  await expect(a.getByText("A group admin approves new members.")).toBeVisible();
  await a.getByLabel("Message to the admins (optional)").fill("colleague of the owner");
  await a.getByRole("button", { name: "Ask to join" }).click();
  await expect(a.getByText("Request sent. You will be told when an admin decides.")).toBeVisible();

  // The owner is told, sees the request in the open panel and approves it.
  await expect(o.locator(".sample-banner")).toContainText("asked to join");
  const request = panel.locator("[data-join-request]", { hasText: "colleague of the owner" });
  await expect(request).toBeVisible();
  await request.getByRole("button", { name: "Approve" }).click();
  await expect(request).toHaveCount(0);

  // The applicant hears it and finds the group in their list.
  await expect(a.locator(".sample-banner")).toContainText("You joined");
  await a.getByRole("button", { name: "Close" }).click();
  await expect(conversationItem(a, group)).toBeVisible();
  await openConversation(a, group);
  await send(a, "approved and in");
  await panel.getByRole("button", { name: "Back" }).click();
  await expectStored(o, "approved and in", false);
});
