import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Viewing a PDF used to let the PDF act on the reader (#1783).
//
// The viewer embedded `<object type="application/pdf">`, which hands the file
// to the browser's own plugin with full honours. A document carrying
// `/OpenAction << /S /Named /N /Print >>` -- the PDF standard's named action
// for "open the print dialog" -- raised that dialog at anyone who opened it.
// It is not JavaScript, which is why nothing else caught it: Chrome ignores a
// PDF's embedded JavaScript and honours its named actions. Sandboxing was not
// available as a fix, because a sandboxed frame cannot instantiate the plugin
// at all.
//
// The viewer is now Mozilla's PDF.js (components/renderers/PdfRenderer.tsx),
// which never executes a document-level action. res-003 "Query Playbook" is
// served by a fixture built to be hostile: two pages, real text, and that open
// action (src/mocks/data/pdfFixture.ts).

const RESOURCES = "/portal/admin/resources";
const RESOURCE = "Query Playbook";
const NEEDLE = "predicate pushdown";

/**
 * Replaces window.print before any page script runs, so the assertion is about
 * a call that was never made rather than about a dialog that never appeared.
 * A real print dialog blocks the browser, and a blocked browser fails the test
 * by timing out, which reports the symptom without naming the cause.
 */
async function trapPrint(page: Page): Promise<void> {
  await page.addInitScript(() => {
    (window as unknown as { __printed: number }).__printed = 0;
    window.print = () => {
      (window as unknown as { __printed: number }).__printed += 1;
    };
  });
}

async function printCount(page: Page): Promise<number> {
  return page.evaluate(() => (window as unknown as { __printed: number }).__printed);
}

async function openPdf(page: Page): Promise<void> {
  await trapPrint(page);
  await authenticate(page);
  await page.goto(RESOURCES);
  await page.getByLabel("Search resources").fill(RESOURCE);
  await page.getByText(RESOURCE, { exact: true }).first().click();
  // The page indicator only appears once the document is open and paginated,
  // so waiting for it is waiting for a rendered document rather than a frame.
  await expect(page.getByTestId("pdf-page-indicator")).toBeVisible({ timeout: 30_000 });
}

test.describe("A PDF cannot act on its reader", () => {
  test("the file really does ask to print itself", async ({ page }) => {
    await authenticate(page);
    // Proves the fixture is hostile, so the cases below are not passing for
    // want of an open action to honour.
    //
    // Fetched from inside the page, not through page.request: the fixtures are
    // served by MSW's service worker, which only sees requests the page makes.
    const served = await page.evaluate(async () => {
      const res = await fetch("/api/v1/resources/res-003/content");
      const buf = new Uint8Array(await res.arrayBuffer());
      let text = "";
      for (const b of buf) text += String.fromCharCode(b);
      return { type: res.headers.get("content-type") ?? "", text };
    });

    expect(served.type).toContain("application/pdf");
    expect(served.text).toContain("/OpenAction");
    expect(served.text).toContain("/S /Named /N /Print");
  });

  test("opening it renders the document and raises no print dialog", async ({ page }) => {
    await openPdf(page);

    await expect(page.getByTestId("pdf-page-indicator")).toHaveText("1 / 2");
    // A rendered page, not just a mounted viewer.
    await expect(page.locator(".pdfViewer canvas").first()).toBeVisible();
    expect(await printCount(page)).toBe(0);
  });

  test("the browser's PDF plugin is no longer in the path", async ({ page }) => {
    await openPdf(page);
    // The `<object>` embed is what honoured the action. Its absence is the
    // structural half of the fix; the print count above is the behavioural half.
    await expect(page.locator('object[type="application/pdf"]')).toHaveCount(0);
  });

  test("the document is readable: paging, text and find", async ({ page }) => {
    await openPdf(page);

    // The text layer is what makes a PDF selectable and searchable. The plugin
    // gave this for free; our viewer has to render it.
    await expect(page.locator(".pdfViewer .textLayer").first()).toBeAttached();

    await page.getByRole("button", { name: "Next page" }).click();
    await expect(page.getByTestId("pdf-page-indicator")).toHaveText("2 / 2");
    await page.getByRole("button", { name: "Previous page" }).click();
    await expect(page.getByTestId("pdf-page-indicator")).toHaveText("1 / 2");

    await page.getByLabel("Find in document").fill(NEEDLE);
    await expect(page.getByTestId("pdf-find-count")).toHaveText("1/1");

    expect(await printCount(page)).toBe(0);
  });

  test("opening one does not hand the portal's theme back to the OS", async ({ page }) => {
    // pdf.js ships `:root { color-scheme: light dark }`, which would undo
    // theme.css and put #1789 back for the rest of the session -- everywhere,
    // not just in the viewer, because the stylesheet stays loaded. lib/
    // pdfViewer.css re-asserts the portal's scheme over it.
    await openPdf(page);
    const scheme = await page.evaluate(() =>
      getComputedStyle(document.documentElement).colorScheme,
    );
    expect(["light", "dark"]).toContain(scheme);
    const dark = await page.evaluate(() => document.documentElement.classList.contains("dark"));
    expect(scheme).toBe(dark ? "dark" : "light");
  });
});
