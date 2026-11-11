import { test, expect } from "@playwright/test";
test("triage, search, burst, deduplication and persistence", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Signal over noise." }),
  ).toBeVisible();
  await expect(page.locator(".metric").nth(1).locator("strong")).toContainText(
    "1,200",
  );
  await page
    .getByRole("button", { name: /Database connection pool exhausted/ })
    .first()
    .click();
  const drawer = page.getByRole("dialog", { name: "Incident detail" });
  await expect(drawer).toBeVisible();
  await drawer
    .getByRole("button", { name: "Acknowledge", exact: true })
    .click();
  await expect(
    drawer.getByText("Acknowledged", { exact: true }).first(),
  ).toBeVisible();
  await drawer.getByRole("button", { name: "Resolve incident" }).click();
  await expect(
    drawer.getByRole("button", { name: "Reopen incident" }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "Similarity search", exact: true })
    .click();
  await page
    .getByRole("textbox", { name: "Search historical incidents" })
    .fill("connection pool");
  await page.getByRole("button", { name: "Find incidents" }).click();
  await expect(page.locator(".match")).toHaveCount(4);
  await page.getByRole("button", { name: "Overview", exact: true }).click();
  await page
    .getByRole("button", { name: "Simulate alerts", exact: true })
    .click();
  await page.getByRole("button", { name: "1,000 alerts", exact: true }).click();
  await page
    .getByRole("button", { name: "Generate 1,000 alerts", exact: true })
    .click();
  await expect(page.locator(".metric").nth(1).locator("strong")).toContainText(
    "2,200",
  );
  await page
    .getByRole("button", { name: "Simulate alerts", exact: true })
    .click();
  await page.getByRole("button", { name: "Test duplicate protection" }).click();
  await expect(page.getByRole("status")).toContainText(
    "1 duplicates safely ignored",
  );
  await page.reload();
  await expect(page.locator(".metric").nth(1).locator("strong")).toContainText(
    "2,201",
  );
  expect(errors).toEqual([]);
});
test("filters stay in URL and empty states work", async ({ page }) => {
  await page.goto("/");
  await page
    .getByRole("textbox", { name: "Filter incidents" })
    .fill("no-match-ever");
  await expect(
    page.getByRole("heading", { name: "No incidents here" }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("textbox", { name: "Filter incidents" }),
  ).toHaveValue("no-match-ever");
  await page.getByRole("textbox", { name: "Filter incidents" }).fill("");
  await page.getByLabel("Filter severity").selectOption("critical");
  await expect(page.locator("tbody tr")).toHaveCount(1);
});
test("mobile navigation and simulator fit viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.getByRole("button", { name: "Open navigation" }).click();
  await page.getByRole("button", { name: "Services", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Services", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Simulate alerts", exact: true })
    .click();
  await expect(
    page.getByRole("dialog", { name: "Alert simulator" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
});
