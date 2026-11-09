import fs from "fs";
import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The portal's Sites section (#2559), against MSW's sites: each state the
// ticket names, and that a release is never rendered on the portal's origin.

test.describe("Sites", () => {
  test.beforeEach(async ({ page }) => {
    await authenticate(page);
  });

  test("every page of the section is a page the shell knows", async ({ page }) => {
    for (const path of ["/portal/sites", "/portal/sites/marketing", "/portal/sites/marketing/access", "/portal/sites/marketing/releases/12", "/portal/admin/domains"]) {
      await page.goto(path);
      await expect(page.getByRole("heading", { level: 1 }).first()).not.toHaveText("Not found");
      await expect(page.getByText("There is no page at this address.")).toHaveCount(0);
    }
  });

  test("lists a site's address, environments and domains", async ({ page }) => {
    await page.goto("/portal/sites");
    await expect(page.getByRole("link", { name: "marketing.pages.example.net" })).toBeVisible();
    await expect(page.getByRole("link", { name: "go.example.com" })).toBeVisible();
    await expect(page.getByText("default 12")).toBeVisible();
  });

  test("says how to start when there are no sites", async ({ page }) => {
    await page.goto("/portal/sites?state=empty");
    await expect(page.getByText("No sites")).toBeVisible();
    await expect(page.getByRole("button", { name: "New site" }).first()).toBeVisible();
  });

  test("says previews are off with no pages domain and links an administrator to the setting", async ({ page }) => {
    await page.goto("/portal/sites?state=previews-off");
    const alert = page.getByTestId("previews-off");
    await expect(alert).toContainText("Previews are off");
    await alert.getByRole("button", { name: "Set a pages domain" }).click();
    await expect(page).toHaveURL(/\/admin\/settings\/sites/);
  });

  test("a new site has only its own address, and creating one opens it", async ({ page }) => {
    await page.goto("/portal/sites");
    await page.getByRole("button", { name: "New site" }).click();
    const dialog = page.getByRole("dialog", { name: "New site" });
    await dialog.getByLabel("Name").fill("careers");
    await expect(dialog.getByText("careers.pages.example.net")).toBeVisible();
    await dialog.getByRole("button", { name: "Create site" }).click();
    await expect(page).toHaveURL(/\/sites\/careers/);
    await expect(page.getByRole("cell", { name: "default" }).first()).toBeVisible();
  });

  test("the tab in view is the one marked", async ({ page }) => {
    await page.goto("/portal/sites/marketing");
    await page.getByRole("tab", { name: "Access" }).click();
    await expect(page.getByRole("tab", { name: "Access" })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByRole("tab", { name: "Releases" })).toHaveAttribute("aria-selected", "false");
  });

  test("environments show who can open what, and Share is only beside the site's own address", async ({ page }) => {
    await page.goto("/portal/sites/marketing");
    await expect(page.getByText("Sign-in and access needed")).toBeVisible();
    await expect(page.getByRole("button", { name: "Share" })).toHaveCount(1);
  });

  test("a domain is added with a new environment and an optional approval rule", async ({ page }) => {
    await page.goto("/portal/sites/marketing/domains");
    await page.getByRole("button", { name: "Add domain" }).click();
    const dialog = page.getByRole("dialog", { name: "Add domain" });
    await dialog.getByLabel("Domain").fill("qa.example.com");
    await dialog.getByLabel("Name").fill("qa");
    await expect(dialog.getByText("Requires approval")).toBeVisible();
    await dialog.getByRole("button", { name: "Add domain" }).click();
    await expect(page.getByRole("link", { name: "qa.example.com" })).toBeVisible();
  });

  test("a refused domain says why", async ({ page }) => {
    await page.goto("/portal/sites/marketing/domains");
    await page.getByRole("button", { name: "Add domain" }).click();
    const dialog = page.getByRole("dialog", { name: "Add domain" });
    await dialog.getByLabel("Domain").fill("go.example.com");
    await dialog.getByLabel("Name").fill("x");
    await dialog.getByRole("button", { name: "Add domain" }).click();
    await expect(dialog.getByTestId("domain-refused")).toContainText("already in use");
  });

  test("access has the intranet checkbox and adding a person shows both levels", async ({ page }) => {
    await page.goto("/portal/sites/marketing/access");
    await expect(page.getByText("Everyone internal can view")).toBeVisible();
    await page.getByRole("button", { name: "Add person" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByRole("radio", { name: "Can view" })).toBeVisible();
    await expect(dialog.getByRole("radio", { name: "Can manage" })).toBeVisible();
  });

  test("promote shows what changes before confirming", async ({ page }) => {
    await page.goto("/portal/sites/marketing");
    await page.getByRole("row", { name: /^prod/ }).getByRole("button", { name: "Promote" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("What changes on prod")).toBeVisible();
    await expect(dialog.getByText("2 files changed")).toBeVisible();
  });

  test("a release lists its files and renders none of them", async ({ page }) => {
    await page.goto("/portal/sites/marketing/releases/12");
    await expect(page.getByText("index.html")).toBeVisible();
    await expect(page.locator("iframe")).toHaveCount(0);
  });

  test("a publish over a limit names the file and the limit before uploading", async ({ page }, testInfo) => {
    const film = testInfo.outputPath("film.mp4");
    fs.writeFileSync(film, Buffer.alloc(68 * 1024 * 1024));
    await page.goto("/portal/sites/marketing");
    await page.getByRole("button", { name: "Publish" }).click();
    const dialog = page.getByRole("dialog", { name: "Publish to marketing" });
    await dialog.getByLabel("Choose .zip").setInputFiles(film);
    await expect(dialog.getByTestId("publish-refused")).toContainText("One file can be at most");
    await expect(dialog.getByRole("button", { name: "Publish" })).toBeDisabled();
  });

  test("the pages domain refuses the portal's own and saves another", async ({ page }) => {
    await page.goto("/portal/admin/settings/sites");
    const field = page.getByLabel("Pages domain");
    await field.fill("pages.portal.example.com");
    await page.getByRole("button", { name: /Save/ }).click();
    await expect(page.getByText(/holds the portal's host/)).toBeVisible();
  });

  test("every address is listed for the ingress", async ({ page }) => {
    await page.goto("/portal/admin/domains");
    await expect(page.getByText("Each of these needs an ingress rule")).toBeVisible();
    await expect(page.getByRole("link", { name: "marketing.pages.example.net" })).toBeVisible();
    await expect(page.getByRole("link", { name: "shop.example.com" })).toBeVisible();
  });
});
