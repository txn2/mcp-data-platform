import { expect, test, type Locator, type Page } from "@playwright/test";

function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set; test/acceptance/issue_2580_test.go and issue_2546_test.go set it`);
  return v;
}

const DESKTOP = { width: 1280, height: 800 };
const PHONE = { width: 375, height: 800 };
// The drawer is max-w-md, 448px, of which its left border takes one.
const DRAWER_WIDTH = 447;
const IDP_LOGIN = /\/realms\/[^/]+\/(protocol\/openid-connect\/auth|login-actions)/;

async function signInWithKey(page: Page): Promise<void> {
  await page.goto("/portal/?login=api_key");
  const key = page.locator('input[placeholder="X-API-Key"]');
  await key.fill(env("FEEDBACK_API_KEY"));
  await page.getByRole("button", { name: "Sign in with API key" }).click();
  // The key is kept for this tab only once it has been validated.
  await expect(key).toHaveCount(0);
}

async function signInWithPassword(page: Page): Promise<void> {
  await page.goto("/portal/auth/login");
  await page.waitForURL(IDP_LOGIN);
  await page.locator("#username").fill(env("FEEDBACK_EMAIL"));
  await page.locator("#password").fill(env("FEEDBACK_PASSWORD"));
  await page.locator("#kc-login").click();
  await page.waitForURL(/\/portal\//);
}

function threadRow(page: Page): Locator {
  return page.getByRole("button", { name: new RegExp(env("FEEDBACK_MARKER")) }).first();
}

/** openInDrawer opens the thread in the Inbox's slide-over (ThreadSlideOver). */
async function openInDrawer(page: Page): Promise<void> {
  await page.goto("/portal/feedback");
  await threadRow(page).click();
  await expect(page.getByRole("button", { name: "Back to list" })).toBeVisible();
}

/** openInPanel opens the thread in the item's own FeedbackPanel. */
async function openInPanel(page: Page): Promise<void> {
  await page.goto(`/portal/assets/${env("FEEDBACK_ASSET")}`);
  await page.getByRole("button", { name: "Feedback", exact: false }).first().click();
  await threadRow(page).click();
  await expect(page.getByRole("button", { name: "Back to list" })).toBeVisible();
}

interface Scroller {
  left: number;
  right: number;
  clientWidth: number;
  scrollWidth: number;
}

/** scroller measures the box that scrolls the thread, found from its header. */
function scroller(page: Page): Promise<Scroller> {
  return page.getByRole("button", { name: "Back to list" }).evaluate((start) => {
    let node = start.parentElement;
    while (node && !["auto", "scroll"].includes(getComputedStyle(node).overflowY)) node = node.parentElement;
    if (!node) throw new Error("the thread has no scroll container");
    const box = node.getBoundingClientRect();
    return { left: box.left, right: box.right, clientWidth: node.clientWidth, scrollWidth: node.scrollWidth };
  });
}

/** expectFits holds a control inside the scroller and clickable where it sits. */
async function expectFits(control: Locator, box: Scroller): Promise<void> {
  await expect(control).toBeVisible();
  const at = await control.boundingBox();
  expect(at, "the control has no box").not.toBeNull();
  expect(at!.x, "the control starts left of the thread").toBeGreaterThanOrEqual(box.left);
  expect(at!.x + at!.width, "the control ends past the thread's edge").toBeLessThanOrEqual(box.right);
  await control.click({ trial: true });
}

async function expectModeratorHeaderFits(page: Page, width?: number): Promise<void> {
  const box = await scroller(page);
  if (width) expect(box.clientWidth).toBe(width);
  expect(box.scrollWidth, "the thread scrolls sideways").toBeLessThanOrEqual(box.clientWidth);
  await expectFits(page.getByRole("button", { name: /^Status:/ }), box);
  await expectFits(page.getByRole("radio", { name: "Internal" }), box);
  await expectFits(page.getByRole("radio", { name: "Shared" }), box);
  await expectFits(page.getByRole("button", { name: "Delete thread" }), box);
  await expect(page.getByRole("radiogroup", { name: "Visibility" })).toBeVisible();
}

/** header is the row holding the way back, where a badge would be. */
function header(page: Page): Locator {
  return page.getByRole("button", { name: "Back to list" }).locator("xpath=../..");
}

for (const [label, viewport, width] of [["448px", DESKTOP, DRAWER_WIDTH], ["375px", PHONE, undefined]] as const) {
  test(`drawer ${label}: a moderator's header fits and shows the visibility once`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openInDrawer(page);
    await expectModeratorHeaderFits(page, width);
    await expect(header(page).getByText(/^(Internal|Shared)$/)).toHaveCount(2);
    await expect(header(page).locator("input[type=radio]")).toHaveCount(2);
    await expect(header(page).locator('[data-slot="badge"]', { hasText: /^(Internal|Shared)$/ })).toHaveCount(0);
  });

  test(`panel ${label}: the item's own feedback panel fits a moderator's header`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openInPanel(page);
    await expectModeratorHeaderFits(page, width);
  });
}

for (const [label, viewport] of [["448px", DESKTOP], ["375px", PHONE]] as const) {
  test(`badge ${label}: someone who cannot change the visibility sees the badge`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithPassword(page);
    await openInDrawer(page);
    const box = await scroller(page);
    expect(box.scrollWidth, "the thread scrolls sideways").toBeLessThanOrEqual(box.clientWidth);
    await expect(header(page).getByText(env("FEEDBACK_VISIBILITY"), { exact: true })).toHaveCount(1);
    await expect(page.getByRole("radiogroup", { name: "Visibility" })).toHaveCount(0);
  });
}

test("capture shortened: a thread over the limit is captured after the reviewer shortens it", async ({ page }) => {
  await signInWithKey(page);
  await openInDrawer(page);
  const field = page.getByLabel("Insight text");
  const capture = page.getByRole("button", { name: "Capture as insight" });

  await expect(field).toHaveValue(new RegExp(env("FEEDBACK_MARKER")));
  await expect(page.getByText("4,357 of 4,000 bytes")).toBeVisible();
  await expect(page.getByText("357 bytes over the limit. Shorten the text to capture this.")).toBeVisible();
  await expect(capture).toBeDisabled();

  await field.fill(env("FEEDBACK_SHORTENED"));
  await expect(page.getByText("The insight is created from this text.")).toBeVisible();
  const posted = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/insight"));
  await capture.click();

  expect((await posted).status()).toBe(201);
  await expect(field).toHaveCount(0);
});

test("capture blocked: text over the limit cannot be submitted and the reviewer sees why", async ({ page }) => {
  const posts: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().endsWith("/insight")) posts.push(r.url());
  });
  await signInWithKey(page);
  await openInDrawer(page);
  const field = page.getByLabel("Insight text");
  const capture = page.getByRole("button", { name: "Capture as insight" });

  // 2,001 characters of two bytes each: under the limit counted in characters.
  await field.fill("é".repeat(2001));
  await expect(page.getByText("4,002 of 4,000 bytes")).toBeVisible();
  await expect(page.getByText("2 bytes over the limit. Shorten the text to capture this.")).toBeVisible();
  await expect(capture).toBeDisabled();

  await field.fill("short");
  await expect(page.getByText("Write at least 10 bytes.")).toBeVisible();
  await expect(capture).toBeDisabled();
  await field.press("Enter");

  expect(posts, "the panel submitted text it refuses").toEqual([]);
});

test("capture refused: the panel shows the server's message", async ({ page }) => {
  await signInWithKey(page);
  await openInDrawer(page);
  const posted = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/insight"));
  await page.getByRole("button", { name: "Capture as insight" }).click();

  expect((await posted).status()).toBe(400);
  await expect(page.getByRole("alert").filter({ hasText: env("FEEDBACK_REFUSAL") })).toBeVisible();
  await expect(page.getByText("Could not capture this as an insight.")).toHaveCount(0);
});

// #2583: the item stays usable while its feedback panel is open.
const ITEM_SIZES = [
  ["1280px", { width: 1280, height: 800 }],
  ["390px", { width: 390, height: 844 }],
] as const;

async function openItemFeedback(page: Page): Promise<void> {
  await page.goto(`/portal/assets/${env("FEEDBACK_ASSET")}`);
  await page.getByRole("main").getByRole("button", { name: /Feedback/ }).first().click();
  await expect(page.getByRole("heading", { name: "Asset feedback" })).toBeVisible();
  await page.getByRole("button", { name: "New", exact: true }).click();
}

function messageBox(page: Page): Locator {
  return page.getByPlaceholder(/Describe your feedback/);
}

/** post sends the feedback and holds that it was the text typed before reading on. */
async function expectKeptAndPost(page: Page, message: string): Promise<void> {
  await expect(messageBox(page)).toHaveValue(message);
  const posted = page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname.endsWith("/threads"));
  await page.getByRole("button", { name: "Post feedback" }).click();
  expect((await posted).status()).toBe(201);
}

for (const [label, viewport] of ITEM_SIZES) {
  test(`asset deck ${label}: the deck pages while feedback is written`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openItemFeedback(page);
    const message = `${env("FEEDBACK_MESSAGE")} ${label}`;
    const box = messageBox(page);
    await box.fill(message);

    const iframe = page.locator('iframe[title="HTML Preview"]');
    const deck = iframe.contentFrame();
    // Where the reader is in the deck. In a frame 435px wide or less the runtime
    // reads as one scrolling page with no next and previous controls, and a
    // step is a scroll of the page; wider, it is a slide.
    const beside = viewport.width >= 1024;
    const position = () =>
      deck.locator("body").evaluate((_, slides) => {
        if (slides) return (window as unknown as { Reveal: { getIndices(): { h: number } } }).Reveal.getIndices().h;
        return document.querySelector(".reveal-viewport")?.scrollTop ?? 0;
      }, beside);
    const atStart = () => expect.poll(position).toBe(0);
    const pastStart = () => expect.poll(position).toBeGreaterThan(0);
    await expect(deck.locator(".slides")).toContainText("Acceptance deck");
    // The text is in the page before the runtime starts; its keys and position exist once it is ready.
    await expect(deck.locator(".reveal.ready")).toBeAttached();
    await atStart();

    // Nothing of the deck is under the panel, beside it or below it. On a narrow
    // screen the page scrolls in the space above the panel and the deck can start
    // below its fold, so it is brought into view first. Where the page happens to
    // be scrolled when this runs depends on when the deck finished loading.
    if (!beside) await iframe.evaluate((el) => el.scrollIntoView({ block: "end" }));
    const frame = (await iframe.boundingBox())!;
    const panel = (await page.locator("[data-item-panel]").boundingBox())!;
    if (beside) {
      expect(frame.x + frame.width, "the panel covers the deck").toBeLessThanOrEqual(panel.x);
    } else {
      // The panel's height is a share of the screen, so its edge can fall between pixels.
      expect(Math.floor(frame.y + frame.height), "the panel covers the deck").toBeLessThanOrEqual(Math.ceil(panel.y));
    }

    // The arrow keys, with the cursor outside the text box.
    await box.evaluate((el) => (el as HTMLElement).blur());
    await page.keyboard.press("ArrowRight");
    await pastStart();
    await page.keyboard.press("ArrowLeft");
    await atStart();

    // Inside the text box the arrow keys move the cursor and the slide stays.
    await box.focus();
    await box.evaluate((el) => (el as HTMLTextAreaElement).setSelectionRange(5, 5));
    await page.keyboard.press("ArrowRight");
    expect(await box.evaluate((el) => (el as HTMLTextAreaElement).selectionStart)).toBe(6);
    await atStart();

    // The deck's own controls, or scrolling the page it has become.
    const next = deck.locator(".controls .navigate-right");
    if (beside) {
      await next.click();
      await pastStart();
      await deck.locator(".controls .navigate-left").click();
      await atStart();
    } else {
      await expect(next).toBeHidden();
      // A wheel or a finger on the frame reaches the deck: nothing lies over it.
      const area = (await iframe.boundingBox())!;
      const tags = await page.evaluate(
        ([x, y, w, h]) =>
          [[x! + 4, y! + 4], [x! + w! - 4, y! + 4], [x! + w! / 2, y! + h! / 2], [x! + 4, y! + h! - 4], [x! + w! - 4, y! + h! - 4]].map(
            ([px, py]) => document.elementFromPoint(px!, py!)?.tagName,
          ),
        [area.x, area.y, area.width, area.height],
      );
      expect(tags, "something lies over the deck").toEqual(["IFRAME", "IFRAME", "IFRAME", "IFRAME", "IFRAME"]);
    }

    await expectKeptAndPost(page, message);
  });

  test(`asset document ${label}: the document scrolls while feedback is written`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await signInWithKey(page);
    await openItemFeedback(page);
    const message = `${env("FEEDBACK_MESSAGE")} ${label}`;
    await messageBox(page).fill(message);

    const main = page.getByRole("main");
    await expect(main.getByText("Paragraph 80 of the long document.")).toBeAttached();
    const area = (await main.boundingBox())!;
    await page.mouse.move(area.x + area.width / 2, area.y + area.height / 2);
    await page.mouse.wheel(0, 600);
    await expect.poll(() => main.evaluate((el) => el.scrollTop), "the document did not scroll").toBeGreaterThan(0);

    await expectKeptAndPost(page, message);
  });
}
