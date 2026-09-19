import { expect, type Frame, type Page } from "@playwright/test";
import { authenticate } from "../../screenshots/helpers/auth";

// Helpers for the two document-theme specs (#1789): an HTML asset follows the
// portal's theme, which follows the OS until the reader picks one.
//
// The OS preference is set by a Chromium launch flag rather than by
// page.emulateMedia or the colorScheme option. Both of those emulate the media
// feature on every frame in the page, so the document would report the
// emulated value whatever the portal did, and the specs would prove nothing.

/** The launch flag that makes Chromium's own preference dark or light. */
export function osScheme(scheme: "dark" | "light") {
  return {
    colorScheme: null,
    launchOptions: { args: [`--blink-settings=preferredColorScheme=${scheme === "dark" ? 0 : 1}`] },
  } as const;
}

/** Sets the stored theme choice before the page loads, as a returning reader has it. */
export async function storeTheme(page: Page, theme: "light" | "dark" | "system"): Promise<void> {
  await page.addInitScript((t) => {
    try {
      localStorage.setItem("mcp-portal-theme", t);
    } catch {
      // localStorage unavailable
    }
  }, theme);
}

/** Opens the mock library's HTML asset and returns the frame its document is in. */
export async function openDocument(page: Page): Promise<Frame> {
  await authenticate(page);
  await page.goto("/portal/assets/ast-deck");
  const frameEl = page.getByTestId("viewer-content").locator("iframe").first();
  await expect(frameEl).toBeVisible();
  const frame = await (await frameEl.elementHandle())!.contentFrame();
  expect(frame, "the viewer's frame has no document").not.toBeNull();
  return frame!;
}

/** Whether the framed document's prefers-color-scheme is dark. */
export function documentSeesDark(frame: Frame): Promise<boolean> {
  return frame.evaluate(() => matchMedia("(prefers-color-scheme: dark)").matches);
}
