import { expect, test } from "@playwright/test";

test("home search form leads to a results page instead of a 404", async ({ page }) => {
  await page.goto("/en/");
  await page.locator("#search-q").fill("zzzz-no-such-event-999");
  await page.getByRole("button", { name: "Search" }).click();
  await expect(page).toHaveURL(/\/en\/events\?q=zzzz-no-such-event-999$/);
  await expect(page.getByRole("heading", { level: 1 })).toContainText("zzzz-no-such-event-999");
  await expect(page.getByRole("heading", { name: "Nothing found" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Filter on the map" })).toBeVisible();
});
