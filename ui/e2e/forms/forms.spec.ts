import { test, expect, type Frame, type Page } from "@playwright/test";

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
  const forms = () => page.frames().filter((f) => f.url().includes("/portal/forms/"));
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
