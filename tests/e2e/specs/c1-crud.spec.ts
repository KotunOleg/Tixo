import { expect, test } from "@playwright/test";

import { e2eOrg } from "../global-setup.ts";
import { datetimeLocal, uniqueSuffix } from "../helpers.ts";

test("organizer creates, edits and deletes a venue and an event (C1 CRUD)", async ({ page }) => {
  const suffix = uniqueSuffix();
  const venueName = `E2E Venue ${suffix}`;
  const eventTitle = `E2E Event ${suffix}`;
  const eventSlug = `e2e-event-${suffix}`;

  await page.goto(`/org/${e2eOrg}/venues/new`);
  await page.locator("#venue-name").fill(venueName);
  await page.locator("#venue-address").fill("1 E2E Street");
  await page.locator("#venue-city").fill("Kyiv");
  await page.locator("#venue-lat").fill("50.45");
  await page.locator("#venue-lng").fill("30.52");
  await page.locator("#venue-capacity").fill("500");
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/venues$`));
  await expect(page.getByText(venueName)).toBeVisible();

  await page.goto(`/org/${e2eOrg}/events/new`);
  await page.locator("#event-title").fill(eventTitle);
  await page.locator("#event-slug").fill(eventSlug);
  await page.locator("#event-venue").selectOption({ label: `${venueName} — Kyiv` });
  await page.locator("#event-starts-at").fill(datetimeLocal(365));
  await page.locator("#event-ends-at").fill(datetimeLocal(366));
  await page.locator("#event-genre").selectOption("rock");
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/events$`));
  await expect(page.getByText(eventTitle)).toBeVisible();

  await page.getByRole("link", { name: eventTitle }).click();
  await page.getByRole("link", { name: "Редагувати" }).click();
  const editedTitle = `${eventTitle} (edited)`;
  await page.locator("#event-title").fill(editedTitle);
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(/\/org\/.+\/events\/\d+$/);
  await expect(page.getByRole("heading", { name: editedTitle })).toBeVisible();

  await page.getByRole("link", { name: "Редагувати" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Видалити подію" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/events$`));
  await expect(page.getByText(editedTitle)).toHaveCount(0);

  await page.goto(`/org/${e2eOrg}/venues`);
  await page
    .getByRole("listitem")
    .filter({ hasText: venueName })
    .getByRole("link", { name: "Редагувати" })
    .click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Видалити майданчик" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/venues$`));
  await expect(page.getByText(venueName)).toHaveCount(0);
});
