import { expect, test, type Page } from "@playwright/test";

// #2435: the Automations list narrows by handler type and badges each row
// with its types, and an owner adds a second schedule from a script's
// Handlers section and sees both.
//
// HANDLERS_BASE_URL is the portal's origin and HANDLERS_API_KEY the key of
// the person who owns the scripts the acceptance file made: HANDLERS_TAG is
// the prefix their names share, HANDLERS_SCHEDULED, HANDLERS_BOTH and
// HANDLERS_NONE the names of a script with a schedule, one with a form and a
// schedule, and one with no handler, and HANDLERS_SCRIPT_ID the script whose
// Handlers section the last case adds a schedule in.

function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set`);
  return v;
}

async function signInWithKey(page: Page): Promise<void> {
  await page.goto("/portal/?login=api_key");
  const key = page.locator('input[placeholder="X-API-Key"]');
  await key.fill(env("HANDLERS_API_KEY"));
  await page.getByRole("button", { name: "Sign in with API key" }).click();
  await expect(key).toHaveCount(0);
}

async function chooseType(page: Page, label: string): Promise<void> {
  await page.getByRole("combobox", { name: "Filter by type" }).click();
  await page.getByRole("option", { name: label, exact: true }).click();
}

test.beforeEach(async ({ page }) => {
  await signInWithKey(page);
  await page.goto("/portal/automations");
  await page.getByRole("textbox", { name: "Search automations" }).first().fill(env("HANDLERS_TAG"));
});

test("criterion 1: Schedule lists exactly the scripts with a schedule handler", async ({ page }) => {
  await chooseType(page, "Schedule");
  await expect(page.getByText(env("HANDLERS_SCHEDULED")).first()).toBeVisible();
  await expect(page.getByText(env("HANDLERS_BOTH")).first()).toBeVisible();
  await expect(page.getByText(env("HANDLERS_NONE"))).toHaveCount(0);
});

test("criterion 2: a script with no handler appears under None", async ({ page }) => {
  await chooseType(page, "None");
  await expect(page.getByText(env("HANDLERS_NONE")).first()).toBeVisible();
  await expect(page.getByText(env("HANDLERS_SCHEDULED"))).toHaveCount(0);
});

test("criterion 3: a form and a schedule show both badges and both filters", async ({ page }) => {
  const row = page.getByRole("row").filter({ hasText: env("HANDLERS_BOTH") });
  await expect(row.getByTestId("handler-types").getByText("Form", { exact: true })).toBeVisible();
  await expect(row.getByTestId("handler-types").getByText("Schedule", { exact: true })).toBeVisible();
  await chooseType(page, "Form");
  await expect(page.getByText(env("HANDLERS_BOTH")).first()).toBeVisible();
  await expect(page.getByText(env("HANDLERS_SCHEDULED"))).toHaveCount(0);
});

test("criterion 4: the owner adds a second schedule from the Handlers section and sees both", async ({ page }) => {
  await page.goto(`/portal/automations/${env("HANDLERS_SCRIPT_ID")}`);
  const section = page.getByTestId("script-handlers");
  await expect(section.getByTestId("handler-row")).toHaveCount(1);
  await section.getByRole("button", { name: "Add handler" }).click();
  await page.getByRole("menuitem", { name: /Schedule/ }).click();
  await section.getByTestId("add-schedule").getByRole("button", { name: "Add schedule" }).click();
  await expect(section.getByTestId("handler-row")).toHaveCount(2);
  await expect(section.getByTestId("handler-row").filter({ hasText: "Schedule" })).toHaveCount(2);
});
