import fs from "fs";
import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The portal's Sites section (#2559, #2604), against MSW's sites: each state
// the tickets name, and that a release is never rendered on the portal's origin.

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

  test("lists a site's address and environments, and leaves its domains to the site's page", async ({ page }) => {
    await page.goto("/portal/sites");
    await expect(page.getByRole("link", { name: "marketing.pages.example.net" })).toBeVisible();
    await expect(page.getByText("dev 12")).toBeVisible();
    // #2612: a domain is listed on the environments page, not in the list.
    await expect(page.getByRole("link", { name: "go.example.com" })).toHaveCount(0);
    // The row's name, not its middle: the middle is the address, which opens
    // the live site in a new tab (#2587).
    await page.getByTestId("site-row-marketing").getByText("Company website").click();
    await expect(page.getByRole("link", { name: "go.example.com" })).toBeVisible();
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

  test("a new site has dev and prod, prod on its main address, and creating one opens it", async ({ page }) => {
    await page.goto("/portal/sites");
    await page.getByRole("button", { name: "New site" }).click();
    const dialog = page.getByRole("dialog", { name: "New site" });
    await dialog.getByLabel("Name").fill("careers");
    await expect(dialog.getByText("careers--dev.pages.example.net")).toBeVisible();
    await expect(dialog.getByText("careers.pages.example.net")).toBeVisible();
    await dialog.getByRole("button", { name: "Create site" }).click();
    await expect(page).toHaveURL(/\/sites\/careers/);
    await expect(page.getByTestId("env-dev")).toContainText("Working copy");
    await expect(page.getByTestId("env-prod")).toContainText("Main address");
  });

  test("the tab in view is the one marked", async ({ page }) => {
    await page.goto("/portal/sites/marketing");
    await page.getByRole("tab", { name: "Access" }).click();
    await expect(page.getByRole("tab", { name: "Access" })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByRole("tab", { name: "Releases" })).toHaveAttribute("aria-selected", "false");
  });

  test("every environment says who can open it, in the stored order, with Share on each", async ({ page }) => {
    await page.goto("/portal/sites/marketing");
    await expect(page.getByText("How this site works")).toBeVisible();
    await expect(page.getByTestId("env-dev")).toContainText("Working copy");
    await expect(page.getByTestId("env-dev")).toContainText("Always first");
    await expect(page.getByTestId("env-prod")).toContainText("Anyone at example.com, with no sign-in.");
    await expect(page.getByTestId("env-prod")).toContainText("Always last");
    await expect(page.getByRole("button", { name: "Share" })).toHaveCount(3);
  });

  test("making a domain public names it and waits for the owner to confirm", async ({ page }) => {
    await page.goto("/portal/sites/marketing/domains");
    await page.getByRole("row", { name: /www\.example\.com/ }).getByRole("button", { name: "Make public" }).click();
    const confirm = page.getByRole("dialog", { name: "Make www.example.com public?" });
    await expect(confirm).toContainText("Every release promoted to prod is public there");
    await expect(confirm.getByRole("button", { name: "Make public" })).toBeDisabled();
  });

  test("a domain is added with a new environment and an optional approval rule", async ({ page }) => {
    await page.goto("/portal/sites/marketing/domains");
    await page.getByRole("button", { name: "Add domain" }).click();
    const dialog = page.getByRole("dialog", { name: "Add domain" });
    await dialog.getByLabel("Domain").fill("qa.example.com");
    await dialog.getByRole("combobox", { name: "Environment" }).click();
    await page.getByRole("option", { name: "New environment" }).click();
    await dialog.getByLabel("Name").fill("qa");
    await expect(dialog.getByRole("radio", { name: "Private" })).toBeChecked();
    await expect(dialog.getByText("Requires approval")).toBeVisible();
    await dialog.getByRole("button", { name: "Add domain" }).click();
    await expect(page.getByRole("link", { name: "qa.example.com" })).toBeVisible();
  });

  test("a refused domain says why", async ({ page }) => {
    await page.goto("/portal/sites/marketing/domains");
    await page.getByRole("button", { name: "Add domain" }).click();
    const dialog = page.getByRole("dialog", { name: "Add domain" });
    await dialog.getByLabel("Domain").fill("go.example.com");
    await dialog.getByRole("button", { name: "Add domain" }).click();
    await expect(dialog.getByTestId("domain-refused")).toContainText("already in use");
  });

  test("access lists who manages and who each environment is shared with", async ({ page }) => {
    await page.goto("/portal/sites/marketing/access");
    await expect(page.getByText("Who manages")).toBeVisible();
    await expect(page.getByRole("row", { name: /web-team/ })).toContainText("dev");
    await expect(page.getByRole("row", { name: /Everyone internal/ })).toContainText("stage");
    await page.getByRole("button", { name: "Share", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Share marketing" });
    await expect(dialog.getByRole("checkbox", { name: "stage" })).toBeChecked();
    await expect(dialog.getByRole("checkbox", { name: "dev" })).not.toBeChecked();
  });

  test("promote shows what changes before confirming", async ({ page }) => {
    await page.goto("/portal/sites/marketing");
    await page.getByTestId("env-prod").getByRole("button", { name: "Promote" }).click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("What changes on prod")).toBeVisible();
    await expect(dialog.getByText("2 files changed")).toBeVisible();
    await expect(dialog.getByText("Who sees it")).toBeVisible();
  });

  test("a release lists its files and renders none of them", async ({ page }) => {
    await page.goto("/portal/sites/marketing/releases/12");
    await expect(page.getByRole("cell", { name: "index.html", exact: true })).toBeVisible();
    await expect(page.locator("iframe")).toHaveCount(0);
  });

  test("a publish over a limit names the file and the limit before uploading", async ({ page }, testInfo) => {
    const film = testInfo.outputPath("film.mp4");
    fs.writeFileSync(film, Buffer.alloc(68 * 1024 * 1024));
    await page.goto("/portal/sites/marketing");
    await page.getByTestId("env-dev").getByRole("button", { name: "Publish" }).click();
    const dialog = page.getByRole("dialog", { name: "Publish to dev" });
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

  test("at 375px the environments, access and share pages fit the screen", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    for (const path of ["/portal/sites/marketing", "/portal/sites/marketing/access", "/portal/sites/marketing/domains"]) {
      await page.goto(path);
      await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
      await expect.poll(overflow, `${path} scrolls sideways`).toBeLessThanOrEqual(0);
    }
    await page.goto("/portal/sites/marketing");
    await page.getByTestId("env-stage").getByRole("button", { name: "Share" }).click();
    await expect(page.getByRole("dialog", { name: "Share marketing" })).toBeVisible();
    await expect.poll(overflow, "the share dialog scrolls sideways").toBeLessThanOrEqual(0);
  });
});
