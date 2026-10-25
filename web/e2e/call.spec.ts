/**
 * Calls (7.3) in the web sample against a real server: the call rings in the
 * other person's tab, is answered, switches to video and is hung up; a second
 * call is declined. The sample host hands out demo credentials, so this is
 * signaling end to end with no audio or video.
 */

import { expect, test, type Page } from "@playwright/test";
import { closeDevices, device, openContact, user } from "./helpers.js";

test.afterAll(closeDevices);

const overlay = (page: Page) => page.locator(".imkit-call");
const status = (page: Page) => page.locator("[data-call-status]");

test("call, answer, switch to video, hang up; then a declined call", async ({ browser }) => {
  const caller = user("call_a");
  const callee = user("call_b");
  const a = await device(browser, caller);
  const b = await device(browser, callee);

  await openContact(a, callee);
  await a.getByRole("button", { name: "Voice call" }).click();
  await expect(status(a)).toHaveText("Calling…");
  // It rings in the callee's tab, with the caller's name from the host.
  await expect(overlay(b)).toHaveAttribute("data-call-phase", "incoming");
  await expect(status(b)).toHaveText("Incoming voice call");
  await expect(overlay(b).locator(".imkit-call-name")).toHaveText(caller);
  await b.getByRole("button", { name: "Answer", exact: true }).click();
  for (const p of [a, b]) await expect(status(p)).toHaveText(/^\d+:\d\d$/);
  await expect(overlay(a).locator(".imkit-call-name")).toHaveText(callee);

  await a.getByRole("button", { name: "Switch to video" }).click();
  await expect(overlay(b)).toContainText("They want to switch to video");
  await b.getByRole("button", { name: "Turn on video" }).click();
  for (const p of [a, b]) await expect(overlay(p).locator(".imkit-call-videos")).toBeVisible();

  await a.getByRole("button", { name: "Hang up" }).click();
  await expect(status(b)).toHaveText("Call ended");
  await expect(overlay(b)).toHaveCount(0); // back to the chat after a moment

  // The callee calls back; the caller declines.
  await openContact(b, caller);
  await b.getByRole("button", { name: "Video call" }).click();
  await expect(status(a)).toHaveText("Incoming video call");
  await a.getByRole("button", { name: "Decline" }).click();
  await expect(status(b)).toHaveText("Declined");
});
