import { expect, test, type Page } from "@playwright/test";

import { e2eOrg } from "../global-setup.ts";
import { datetimeLocal, uniqueSuffix } from "../helpers.ts";

async function publishEventWithTier(page: Page, suffix: string, title: string, slug: string) {
  const venueName = `E2E Assistant Hall ${suffix}`;
  await page.goto(`/org/${e2eOrg}/venues/new`);
  await page.locator("#venue-name").fill(venueName);
  await page.locator("#venue-address").fill("7 Prompt Street");
  await page.locator("#venue-city").fill("Kyiv");
  await page.locator("#venue-lat").fill("50.45");
  await page.locator("#venue-lng").fill("30.52");
  await page.locator("#venue-capacity").fill("300");
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/venues$`));

  await page.goto(`/org/${e2eOrg}/events/new`);
  await page.locator("#event-title").fill(title);
  await page.locator("#event-slug").fill(slug);
  await page.locator("#event-venue").selectOption({ label: `${venueName} — Kyiv` });
  await page.locator("#event-starts-at").fill(datetimeLocal(2));
  await page.locator("#event-ends-at").fill(datetimeLocal(3));
  await page.locator("#event-genre").selectOption("rock");
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/events$`));

  await page.getByRole("link", { name: title }).click();
  await page.locator("#new-tier-name").fill("Фан-зона");
  await page.locator("#new-tier-price").fill("500");
  await page.locator("#new-tier-quantity").fill("50");
  await page.locator("#new-tier-name").press("Enter");
  await page.getByRole("button", { name: "Опублікувати" }).click();
  await expect(page.getByRole("button", { name: "Зняти з публікації" })).toBeVisible();
}

async function ask(page: Page, text: string) {
  await page.locator("#assistant-input").fill(text);
  await page.locator("#assistant-input").press("Enter");
  await expect(page.getByRole("button", { name: "Надіслати" })).toBeVisible({ timeout: 15000 });
}

test("assistant streams its steps and answer, and a buy request ends at a card the user confirms (C21)", async ({ page }) => {
  const suffix = uniqueSuffix();
  const marker = `zxq${suffix}`;
  const title = `${marker} <img src=x onerror="window.__xss=1">`;
  const slug = `e2e-assistant-${suffix}`;
  await publishEventWithTier(page, suffix, title, slug);

  await page.goto("/assistant");
  await ask(page, marker);
  const reply = page.locator(".tixo-msg-ai").last();
  await expect(reply.locator(".tixo-agent-steps summary")).toBeVisible();
  await reply.locator(".tixo-agent-steps summary").click();
  await expect(reply.locator(".tixo-agent-step-list li").first()).toContainText("Шукаю події");
  await expect(reply.locator(".tixo-msg-text")).toContainText(marker);
  await expect(reply.getByRole("link", { name: title })).toHaveAttribute("href", `/events/${slug}`);
  expect(await page.locator(".js-assistant img").count()).toBe(0);
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();

  await ask(page, `купи квиток ${marker}`);
  const card = page.locator(".tixo-confirm").last();
  await expect(card).toContainText(title);
  await expect(card).toContainText("Фан-зона");
  await card.getByRole("button", { name: "Купити" }).click();
  await expect(page).toHaveURL(/\/orders\/\d+$/);
  await expect(page.getByText(title).first()).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
});

test("assistant page asks guests to sign in (C21)", async ({ browser }) => {
  const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const page = await context.newPage();
  await page.goto("/en/assistant");
  await expect(page.getByRole("link", { name: "Sign in to ask" })).toBeVisible();
  await expect(page.locator("#assistant-input")).toHaveCount(0);
  await context.close();
});
