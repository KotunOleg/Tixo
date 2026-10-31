import { expect, test } from "@playwright/test";

import { escapeRegExp } from "../helpers.ts";

const demoEventSlug = process.env.E2E_SEO_EVENT_SLUG ?? "okean-elzy-palats-sportu";

test.use({ storageState: { cookies: [], origins: [] } });

test("public event page carries SEO metadata (C7)", async ({ page, baseURL }) => {
  const response = await page.goto(`/events/${demoEventSlug}`);
  expect(response?.status()).toBe(200);

  const title = await page.title();
  expect(title.length).toBeGreaterThan(0);

  const canonical = page.locator('link[rel="canonical"]');
  await expect(canonical).toHaveAttribute("href", new RegExp(`/events/${demoEventSlug}$`));

  const pageTitlePrefix = title.split(" · ")[0] ?? title;
  const ogTitle = page.locator('meta[property="og:title"]');
  await expect(ogTitle).toHaveAttribute("content", new RegExp(escapeRegExp(pageTitlePrefix)));

  const ogDescription = page.locator('meta[property="og:description"]');
  await expect(ogDescription).toHaveAttribute("content", /.+/);

  const ogURL = page.locator('meta[property="og:url"]');
  await expect(ogURL).toHaveAttribute("content", new RegExp(`/events/${demoEventSlug}$`));

  const jsonLd = page.locator('script[type="application/ld+json"]');
  const raw = await jsonLd.textContent();
  expect(raw).toBeTruthy();
  const parsed = JSON.parse(raw ?? "{}");
  expect(parsed["@type"]).toBe("Event");
  expect(typeof parsed.name).toBe("string");
  expect(parsed.name.length).toBeGreaterThan(0);
  expect(parsed.startDate).toBeTruthy();
  expect(parsed.location?.["@type"]).toBe("Place");

  const sitemap = await page.request.get(new URL("/sitemap.xml", baseURL).toString());
  expect(sitemap.status()).toBe(200);

  const robots = await page.request.get(new URL("/robots.txt", baseURL).toString());
  expect(robots.status()).toBe(200);
});
