import { expect, test } from "@playwright/test";

import { e2eOrg } from "../global-setup.ts";
import { datetimeLocal, uniqueSuffix } from "../helpers.ts";

test("lineup search autocompletes from the backend after 3 characters (F2)", async ({ page }) => {
  const suffix = uniqueSuffix();
  const venueName = `E2E F2 Venue ${suffix}`;
  const eventTitle = `E2E F2 Event ${suffix}`;
  const eventSlug = `e2e-f2-event-${suffix}`;

  await page.goto(`/org/${e2eOrg}/venues/new`);
  await page.locator("#venue-name").fill(venueName);
  await page.locator("#venue-address").fill("1 E2E Street");
  await page.locator("#venue-city").fill("Kyiv");
  await page.locator("#venue-lat").fill("50.45");
  await page.locator("#venue-lng").fill("30.52");
  await page.locator("#venue-capacity").fill("500");
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/venues$`));

  await page.goto(`/org/${e2eOrg}/events/new`);
  await page.locator("#event-title").fill(eventTitle);
  await page.locator("#event-slug").fill(eventSlug);
  await page.locator("#event-venue").selectOption({ label: `${venueName} — Kyiv` });
  await page.locator("#event-starts-at").fill(datetimeLocal(365));
  await page.locator("#event-ends-at").fill(datetimeLocal(366));
  await page.locator("#event-genre").selectOption("rock");
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/events$`));

  await page.getByRole("link", { name: eventTitle }).click();
  await expect(page).toHaveURL(/\/org\/.+\/events\/\d+$/);

  let searchRequests = 0;
  await page.route("**/lineup/search**", (route) => {
    searchRequests += 1;
    void route.continue();
  });

  await page.locator("#select2-lineup-artist-select-container").click();
  const searchField = page.locator("input.select2-search__field");
  await searchField.fill("Ок");
  await page.waitForTimeout(400);
  expect(searchRequests, "a 2-character query must not reach the backend (minimumInputLength=3)").toBe(0);

  await searchField.fill("Океан");
  await page.waitForResponse((res) => res.url().includes("/lineup/search") && res.status() === 200);
  expect(searchRequests).toBeGreaterThan(0);

  const hit = page.locator(".select2-results__option", { hasText: "Океан Ельзи" }).first();
  await expect(hit).toBeVisible();
  await Promise.all([page.waitForEvent("load"), hit.click()]);

  await expect(page.locator(".list-group-item", { hasText: "Океан Ельзи" })).toBeVisible();
});
