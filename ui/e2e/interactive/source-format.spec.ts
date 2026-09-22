import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test, expect, type Page, type Request } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The Source view of a single-line HTML asset (#1839). ast-oneline is the mock
// library's report saved the way an agent saves one: every element on one line,
// inline <style> and <script> included. These run in a browser because the
// claims are about what a browser does: the editor's width decides whether it
// opens wrapped, the Preview is what must not change when the source is
// formatted, and Format checks its own HTML output by laying both versions out.

const here = path.dirname(fileURLToPath(import.meta.url));
const fixture = (name: string) =>
  readFileSync(path.join(here, "../../src/test/fixtures/source-format", name), "utf8");

const viewToggle = (page: Page) => page.getByRole("group", { name: "Content view" });
const editor = (page: Page) => page.locator(".cm-editor");

/** Every content write the page sends, recorded from the moment it opens. */
function recordContentWrites(page: Page): Request[] {
  const writes: Request[] = [];
  page.on("request", (req) => {
    if (req.method() === "PUT" && /\/assets\/ast-oneline\/content$/.test(req.url())) writes.push(req);
  });
  return writes;
}

/** Every Prettier module the page fetches. */
function recordPrettierLoads(page: Page): string[] {
  const loads: string[] = [];
  page.on("request", (req) => {
    if (/prettier/.test(req.url())) loads.push(req.url());
  });
  return loads;
}

async function openReport(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/assets/ast-oneline");
  await expectReportRendered(page);
}

/** Waits for the preview frame to have run the report's script. */
async function expectReportRendered(page: Page): Promise<void> {
  const frame = page.getByTestId("viewer-content").frameLocator("iframe");
  await expect(frame.locator("#bars .bar")).toHaveCount(4);
  await expect(frame.locator("#detail tbody tr")).toHaveCount(4);
}

/**
 * The rendered document, inside the frame. The frame element's own rounded
 * border is portal chrome and antialiases differently from one paint to the
 * next, so it is left out.
 */
async function previewShot(page: Page): Promise<Buffer> {
  return page
    .getByTestId("viewer-content")
    .frameLocator("iframe")
    .locator("html")
    .screenshot({ animations: "disabled" });
}

/** Replaces the whole buffer, as a person selecting all and pasting would. */
async function replaceBuffer(page: Page, text: string): Promise<void> {
  await page.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+a");
  await page.keyboard.insertText(text);
}

test("a single-line report opens wrapped in Source, with nothing written", async ({ page }) => {
  const writes = recordContentWrites(page);
  await openReport(page);

  await viewToggle(page).getByRole("button", { name: "Source" }).click();

  await expect(page.getByRole("button", { name: "Wrap" })).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator(".cm-content")).toHaveClass(/cm-lineWrapping/);
  // Wrapped: nothing runs past the right edge.
  const overflow = await editor(page).locator(".cm-scroller").evaluate((el) => el.scrollWidth - el.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);

  // Unwrapping is the reader's choice, and it is the one-line document again.
  await page.getByRole("button", { name: "Wrap" }).click();
  await expect(page.locator(".cm-content")).not.toHaveClass(/cm-lineWrapping/);
  const unwrapped = await editor(page).locator(".cm-scroller").evaluate((el) => el.scrollWidth - el.clientWidth);
  expect(unwrapped).toBeGreaterThan(1000);

  // Switching views writes nothing, and Save has nothing to store.
  await viewToggle(page).getByRole("button", { name: "Preview" }).click();
  await expectReportRendered(page);
  await viewToggle(page).getByRole("button", { name: "Source" }).click();
  await expect(page.getByRole("button", { name: "Save" })).toBeDisabled();
  expect(writes).toHaveLength(0);
});

test("Format reindents the report, Save stores it, and the preview renders the same", async ({ page }) => {
  const writes = recordContentWrites(page);
  const prettier = recordPrettierLoads(page);
  await openReport(page);
  const before = await previewShot(page);

  await viewToggle(page).getByRole("button", { name: "Source" }).click();
  await expect(editor(page)).toBeVisible();
  // The formatter is not part of the page until Format is pressed.
  expect(prettier).toHaveLength(0);

  await page.getByRole("button", { name: "Format" }).click();

  // The doctype is a line of its own now; the stored body below shows the rest.
  await expect(page.locator(".cm-line").first()).toHaveText("<!DOCTYPE html>");
  expect(prettier.length).toBeGreaterThan(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  // Formatted, not saved: the document is dirty and nothing has been written.
  await expect(page.getByRole("button", { name: "Save" })).toBeEnabled();
  expect(writes).toHaveLength(0);

  await page.getByRole("button", { name: "Save" }).click();
  const summary = page.getByRole("dialog", { name: "What changed?" });
  await summary.getByRole("textbox", { name: "Change summary" }).fill("Formatted the source");
  const saved = page.waitForResponse(
    (res) => res.request().method() === "PUT" && /\/assets\/ast-oneline\/content$/.test(res.url()),
  );
  await summary.getByRole("button", { name: "Save" }).click();
  expect((await saved).ok()).toBe(true);
  expect(writes).toHaveLength(1);
  const stored = writes[0]!.postData() ?? "";
  expect(stored).toContain('\n  <head>\n    <meta charset="utf-8" />');
  expect(stored).toContain("      .kpis {\n        display: grid;\n");
  expect(stored).toContain("\n      const total = rows.reduce((s, r) => s + r.revenue, 0);\n");

  // A save returns the page to Preview, rendering the stored version.
  await expect(viewToggle(page).getByRole("button", { name: "Preview" })).toHaveAttribute("aria-pressed", "true");
  await expectReportRendered(page);
  const after = await previewShot(page);
  expect(after.equals(before), "the preview changed after Format + Save").toBe(true);
});

test("Format refuses output that would display differently, and leaves the buffer", async ({ page }) => {
  const writes = recordContentWrites(page);
  await openReport(page);
  await viewToggle(page).getByRole("button", { name: "Source" }).click();

  // An element the page's own stylesheet sets to white-space: pre. Prettier
  // reflows its text; the render check sees the difference.
  const whitespace = fixture("whitespace.html");
  await replaceBuffer(page, whitespace);
  await page.getByRole("button", { name: "Format" }).click();

  await expect(page.getByRole("alert")).toContainText("would change how this document's text displays");
  await expect(page.locator(".cm-line")).toHaveCount(whitespace.split("\n").length);
  expect(writes).toHaveLength(0);
});

test("Format leaves unparseable HTML as it was and names the problem", async ({ page }) => {
  await openReport(page);
  await viewToggle(page).getByRole("button", { name: "Source" }).click();

  await replaceBuffer(page, "<p>unclosed</div>");
  await page.getByRole("button", { name: "Format" }).click();

  await expect(page.getByRole("alert")).toContainText("Unexpected closing tag");
  await expect(page.locator(".cm-line")).toHaveCount(1);
  await expect(page.locator(".cm-line").first()).toHaveText("<p>unclosed</div>");
});

test("undo takes a Format back, and the document is unchanged again", async ({ page }) => {
  const writes = recordContentWrites(page);
  await openReport(page);
  await viewToggle(page).getByRole("button", { name: "Source" }).click();

  await page.getByRole("button", { name: "Format" }).click();
  await expect(page.locator(".cm-line").first()).toHaveText("<!DOCTYPE html>");
  await expect(page.getByRole("button", { name: "Save" })).toBeEnabled();
  // Wrapping in between is a change of display, not of history.
  await page.getByRole("button", { name: "Wrap" }).click();

  await page.locator(".cm-content").click();
  await page.keyboard.press("ControlOrMeta+z");

  await expect(page.locator(".cm-line").first()).toContainText('<!DOCTYPE html><html lang="en"><head>');
  await expect(page.getByRole("button", { name: "Save" })).toBeDisabled();
  expect(writes).toHaveLength(0);
});
