import { test, expect, type Frame, type Page } from "@playwright/test";
import { embedSnippet } from "../../src/pages/scripts/endpoints/embedSnippet";

// #2177 and #2180: forms an asset embeds, as an anonymous browser meets them
// on a public share, and the form page itself. Each case names the criterion
// it is; the Go criterion that runs it supplies the environment below.
const env = (name: string): string => {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; this suite is run by test/acceptance/issue_2177_test.go and issue_2180_test.go`);
  return v;
};

/**
 * The frames a form page is drawn in, once there are want of them. A page
 * that never draws them fails naming every frame it has and what its console
 * said, which is where a frame the page's policy refused is reported.
 */
async function formFrames(page: Page, want: number, consoleErrors: string[]): Promise<Frame[]> {
  const forms = () => page.frames().filter((f) => f.url().includes("/forms/"));
  const deadline = Date.now() + 30_000;
  while (forms().length < want && Date.now() < deadline) await page.waitForTimeout(250);
  if (forms().length < want) {
    const frames = page.frames().map((f) => f.url() || "(no address)");
    throw new Error(`${forms().length} of ${want} form frames drew; frames: ${frames.join(", ")}; console: ${consoleErrors.join(" | ")}`);
  }
  return forms();
}

/** Fills a form's email and message, waits out its fill time and submits. */
async function submitIn(frame: Frame, email: string) {
  await frame.locator("#f-email").waitFor();
  await frame.locator("#f-email").fill(email);
  await frame.locator("#f-message").fill("Sent from the shared page");
  await frame.waitForTimeout(2500); // the platform's minimum fill time is 2 seconds
  await frame.getByRole("button", { name: "Submit" }).click();
  await expect(frame.getByText(env("FORMS_CONFIRMATION"))).toBeVisible();
}

test("criterion share: an anonymous browser submits every form a shared asset embeds", async ({ browser }) => {
  // A fresh context carries no session: the reader is anonymous.
  const context = await browser.newContext();
  const page = await context.newPage();
  const consoleErrors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });
  await page.goto(env("FORMS_SHARE_PATH"));
  const assetFrame = page.locator("iframe[title='HTML Preview']");
  await expect(assetFrame).toHaveAttribute("sandbox", "allow-scripts allow-downloads");
  const frames = await formFrames(page, Number(env("FORMS_COUNT")), consoleErrors);
  for (const [i, frame] of frames.entries()) {
    expect(new URL(frame.url()).searchParams.get("t")).toBe(env("FORMS_TOKEN"));
    await submitIn(frame, `anon${i}@example.com`);
  }
  await context.close();
});

const SIZES = { desktop: { width: 1280, height: 900 }, phone: { width: 390, height: 844 } } as const;

test("criterion screens: the form page in light and dark at desktop and phone width", async ({ browser }) => {
  for (const scheme of ["light", "dark"] as const) {
    for (const [size, viewport] of Object.entries(SIZES)) {
      const context = await browser.newContext({ colorScheme: scheme, viewport });
      const page = await context.newPage();
      await page.goto(env("FORMS_FORM_PATH"));
      await page.locator("#f-email").waitFor();
      // A refused submission shows the field errors the platform answered with.
      await page.locator("#f-email").fill("not-an-address");
      await page.waitForTimeout(2500);
      await page.getByRole("button", { name: "Submit" }).click();
      await expect(page.locator("#f-email-error")).toBeVisible();
      await page.screenshot({ path: `${env("FORMS_SCREENSHOT_DIR")}/form-${scheme}-${size}.png`, fullPage: true });
      await context.close();
    }
  }
});

// #2331: the hosted page, opened at its own address by an anonymous browser
// with a query string, submitted and confirmed.
test("criterion hosted: an anonymous browser submits a form's hosted page", async ({ browser }) => {
  const context = await browser.newContext();
  const page = await context.newPage();
  await page.goto(env("FORMS_FORM_PATH"));
  await page.locator("#f-email").fill("hosted@example.com");
  await page.locator("#f-message").fill("Sent from the hosted page");
  await page.waitForTimeout(2500); // the platform's minimum fill time is 2 seconds
  await page.getByRole("button", { name: "Submit" }).click();
  await expect(page.getByText(env("FORMS_CONFIRMATION"))).toBeVisible();
  await context.close();
});

// #2331: the embed snippet the automation's page hands out, on a site the
// binding lists and on one it does not. Playwright answers both sites itself;
// the frames load the platform's page.
test("criterion snippet: the snippet works on a listed site and is refused on another", async ({ browser, baseURL }) => {
  const formURL = new URL(env("FORMS_FORM_PATH"), baseURL).toString();
  const site = (_origin: string) =>
    `<!doctype html><html><head><title>A site</title></head><body><h1>Contact us</h1>${embedSnippet(formURL, "contact")}</body></html>`;

  const listed = await browser.newContext();
  const page = await listed.newPage();
  const said: string[] = [];
  page.on("console", (m) => said.push(`${m.type()}: ${m.text()}`));
  page.on("requestfailed", (r) => said.push(`failed: ${r.url()} ${r.failure()?.errorText ?? ""}`));
  await page.route(`${env("FORMS_LISTED_ORIGIN")}/**`, (route) => route.fulfill({ contentType: "text/html", body: site(env("FORMS_LISTED_ORIGIN")) }));
  await page.goto(`${env("FORMS_LISTED_ORIGIN")}/contact.html`);
  const frame = page.frameLocator("iframe[data-platform-form]");
  try {
    await expect(frame.locator("#f-email")).toBeVisible({ timeout: 30_000 });
  } catch (e) {
    const frames = page.frames().map((f) => f.url() || "(no address)");
    throw new Error(`the listed site's frame drew no form; frames: ${frames.join(", ")}; console: ${said.join(" | ")}`, { cause: e });
  }
  await frame.locator("#f-email").fill("embedded@example.com");
  await frame.locator("#f-message").fill("Sent from a site");
  await expect.poll(() => page.locator("iframe[data-platform-form]").evaluate((el) => (el as HTMLIFrameElement).style.height)).toMatch(/^\d+px$/);
  await page.waitForTimeout(2500);
  await frame.getByRole("button", { name: "Submit" }).click();
  await expect(frame.getByText(env("FORMS_CONFIRMATION"))).toBeVisible();
  await listed.close();

  const unlisted = await browser.newContext();
  const other = await unlisted.newPage();
  const refusals: string[] = [];
  other.on("console", (m) => {
    if (m.type() === "error") refusals.push(m.text());
  });
  await other.route(`${env("FORMS_UNLISTED_ORIGIN")}/**`, (route) => route.fulfill({ contentType: "text/html", body: site(env("FORMS_UNLISTED_ORIGIN")) }));
  await other.goto(`${env("FORMS_UNLISTED_ORIGIN")}/contact.html`);
  await other.waitForTimeout(5000);
  await expect(other.frameLocator("iframe[data-platform-form]").locator("#f-email")).toHaveCount(0);
  expect(refusals.join(" | "), "the browser names the policy that refused the frame").toContain("frame-ancestors");
  await unlisted.close();
});
