/**
 * The admin pages (8.1) in the web sample against a real server: a call
 * between two users shows up in the call records with its steps, and a
 * superadministrator reads their chat and clears it for both, which empties
 * it on the users' screens too. The sample host signs administrators in at
 * /dev/admin-login and records calls with recordingCalls.
 */

import { expect, test } from "@playwright/test";
import { blankPage, closeDevices, device, expectStored, openContact, openConversation, row, send, user } from "./helpers.js";

test.afterAll(closeDevices);

test("a call in the call records; a chat read and cleared for both users", async ({ browser }) => {
  const caller = user("adm_a");
  const callee = user("adm_b");
  const a = await device(browser, caller);
  const b = await device(browser, callee);
  const status = (p: typeof a) => p.locator("[data-call-status]");

  await openContact(a, callee);
  await send(a, "something to clear");
  await expectStored(a, "something to clear", true);
  await a.getByRole("button", { name: "Voice call" }).click();
  await b.getByRole("button", { name: "Answer", exact: true }).click();
  for (const p of [a, b]) await expect(status(p)).toHaveText(/^\d+:\d\d$/);
  await a.getByRole("button", { name: "Hang up" }).click();
  await expect(status(b)).toHaveText("Call ended");
  await expect(b.locator(".imkit-call")).toHaveCount(0);
  await openConversation(b, caller);
  await expectStored(b, "something to clear", false);

  // The superadministrator, in a browser of their own.
  const admin = await blankPage(browser);
  await admin.goto("/#/admin/im/calls");
  await admin.locator('input[name="name"]').fill("root");
  await admin.locator('select[name="role"]').selectOption("superadmin");
  await admin.getByRole("button", { name: "Sign in" }).click();
  await expect(admin.getByTestId("admin")).toHaveText("admin-root · superadmin");

  // Records pile up across runs: this run's users have one call between them.
  const callerId = `dev-${caller}`;
  await admin.getByLabel("User ID").fill(callerId);
  await admin.getByRole("button", { name: "Apply" }).click();
  const calls = admin.locator("tr[data-call-id]");
  await expect(calls).toHaveCount(1);
  await expect(calls).toContainText(caller);
  await expect(calls).toContainText(callee);
  await expect(calls).toContainText("Ended");
  await calls.getByRole("button", { name: "Details" }).click();
  await expect(admin.getByRole("heading", { name: `${caller} → ${callee}` })).toBeVisible();
  const timeline = admin.locator(".imkit-admin-timeline");
  await expect(timeline).toContainText("Answered");
  await expect(timeline).toContainText("Ended");

  await admin.getByRole("link", { name: "Chat records" }).click();
  await admin.getByLabel("User ID").fill(callerId);
  await admin.getByRole("button", { name: "Search" }).click();
  await admin.getByRole("button", { name: new RegExp(callee) }).click();
  await expect(admin.locator(".imkit-admin-message-list")).toContainText("something to clear");

  await admin.getByRole("button", { name: "Clear chat records" }).click();
  const dialog = admin.getByRole("dialog", { name: "Clear this conversation for both users?" });
  const topic = (await dialog.locator(".imkit-admin-mono").textContent())!;
  const go = dialog.getByRole("button", { name: "Clear chat records" });
  await dialog.getByLabel("Reason").fill("e2e: the user asked");
  await expect(go).toBeDisabled();
  await dialog.getByLabel(`Type CLEAR ${topic} to confirm`).fill(`CLEAR ${topic}`);
  await go.click();
  await expect(dialog.getByRole("status")).toHaveText(/^Deleted [1-9]\d* messages and \d+ files\.$/);
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(admin.getByText("No messages")).toBeVisible();

  // Both users' screens follow (topic_cleared).
  await expect(row(a, "something to clear")).toHaveCount(0);
  await expect(row(b, "something to clear")).toHaveCount(0);
});
