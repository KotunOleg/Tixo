import { expect, test, type Page } from "@playwright/test";

import { e2eOrg } from "../global-setup.ts";
import { datetimeLocal, uniqueSuffix } from "../helpers.ts";

const pins = (page: Page) => page.locator(".js-filter-map .tixo-map-pin");
const venues = (page: Page) => page.locator(".js-map-results .tixo-venue-result");

async function applyFilter(page: Page, change: () => Promise<unknown>) {
  const response = page.waitForResponse((r) => r.url().includes("/map/venues.json"));
  await change();
  expect((await response).status()).toBe(200);
}

test.describe("public venue map (C8)", () => {
  test.use({ storageState: { cookies: [], origins: [] } });

  test("genre and price filters redraw markers and the list without reloading the page", async ({ page }) => {
    await page.goto("/map");
    await expect(pins(page).first()).toBeVisible();
    const all = await pins(page).count();
    expect(all).toBeGreaterThanOrEqual(10);
    await expect(venues(page)).toHaveCount(all);

    await page.evaluate(() => {
      (window as unknown as { __tixoSamePage?: boolean }).__tixoSamePage = true;
    });

    await applyFilter(page, () => page.locator("#map-genre").selectOption("jazz"));
    await expect(pins(page)).not.toHaveCount(all);
    const jazz = await pins(page).count();
    expect(jazz).toBeGreaterThan(0);
    expect(jazz).toBeLessThan(all);
    await expect(venues(page)).toHaveCount(jazz);
    await expect(page).toHaveURL(/\/map\?genre=jazz$/);

    await applyFilter(page, () => page.locator("#map-genre").selectOption(""));
    await applyFilter(page, () => page.locator("#map-max-price").fill("0"));
    const free = await pins(page).count();
    expect(free).toBeGreaterThan(0);
    expect(free).toBeLessThan(all);
    const facts = await page.locator(".js-map-results .tixo-venue-events li span").allTextContents();
    expect(facts.length).toBeGreaterThan(0);
    for (const line of facts) {
      expect(line).toContain("Безкоштовно");
    }

    expect(await page.evaluate(() => (window as unknown as { __tixoSamePage?: boolean }).__tixoSamePage)).toBe(true);
  });

  test("a filter with no matches shows the empty state and no markers", async ({ page }) => {
    await page.goto("/map");
    await expect(pins(page).first()).toBeVisible();

    await applyFilter(page, () => page.locator("#map-genre").selectOption("metal"));
    await applyFilter(page, () => page.locator("#map-max-price").fill("0"));

    await expect(pins(page)).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Нічого не знайдено" })).toBeVisible();
    await expect(page.locator(".js-map-results").getByRole("link", { name: "Скинути фільтри" })).toBeVisible();
    await expect(page.locator(".js-map-reset")).toBeHidden();
  });

  test("a marker opens its popup from the keyboard", async ({ page }) => {
    await page.goto("/map");
    const first = pins(page).first();
    await expect(first).toBeVisible();
    const name = await first.getAttribute("aria-label");
    await first.focus();
    await page.keyboard.press("Enter");
    const popup = page.locator(".leaflet-popup");
    await expect(popup).toBeVisible();
    await expect(popup.locator(".tixo-map-popup-title")).toHaveText(name ?? "");
    await expect(popup.getByRole("link").first()).toHaveAttribute("href", /^\/events\//);
  });
});

test("markup in an event title stays text inside the map popup", async ({ page, browser }) => {
  const suffix = uniqueSuffix();
  const venueName = `E2E Map Venue ${suffix}`;
  const title = `<img src=x onerror="window.__tixoXSS=1">${suffix}`;
  const slug = `e2e-map-${suffix}`;

  await page.goto(`/org/${e2eOrg}/venues/new`);
  await page.locator("#venue-name").fill(venueName);
  await page.locator("#venue-address").fill("1 E2E Street");
  await page.locator("#venue-city").fill("Kherson");
  await page.locator("#venue-lat").fill("46.6354");
  await page.locator("#venue-lng").fill("32.6169");
  await page.locator("#venue-capacity").fill("100");
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/venues$`));

  await page.goto(`/org/${e2eOrg}/events/new`);
  await page.locator("#event-title").fill(title);
  await page.locator("#event-slug").fill(slug);
  await page.locator("#event-venue").selectOption({ label: `${venueName} — Kherson` });
  await page.locator("#event-starts-at").fill(datetimeLocal(400));
  await page.locator("#event-ends-at").fill(datetimeLocal(401));
  await page.locator("#event-genre").selectOption("metal");
  await page.getByRole("button", { name: "Зберегти" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/events$`));
  await page.getByRole("link", { name: title }).click();
  const eventURL = page.url();
  await page.getByRole("button", { name: "Опублікувати" }).click();

  const guest = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  try {
    const map = await guest.newPage();
    await map.goto("/map");
    await expect(pins(map).first()).toBeVisible();
    await applyFilter(map, () => map.locator("#map-genre").selectOption("metal"));
    await applyFilter(map, () => map.locator("#map-from").fill(datetimeLocal(399).slice(0, 10)));
    const pin = map.locator(`.tixo-map-pin[aria-label="${venueName}"]`);
    await expect(pin).toHaveCount(1);
    await pin.focus();
    await map.keyboard.press("Enter");
    const popup = map.locator(".leaflet-popup");
    await expect(popup.getByRole("link", { name: title })).toBeVisible();
    await expect(popup.locator("img")).toHaveCount(0);
    expect(await map.evaluate(() => (window as unknown as { __tixoXSS?: number }).__tixoXSS)).toBeUndefined();
  } finally {
    await guest.close();
  }

  await page.goto(eventURL);
  await page.getByRole("button", { name: "Зняти з публікації" }).click();
  await page.getByRole("link", { name: "Редагувати" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Видалити подію" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/events$`));
  await page.goto(`/org/${e2eOrg}/venues`);
  await page.getByRole("listitem").filter({ hasText: venueName }).getByRole("link", { name: "Редагувати" }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Видалити майданчик" }).click();
  await expect(page).toHaveURL(new RegExp(`/org/${e2eOrg}/venues$`));
});
