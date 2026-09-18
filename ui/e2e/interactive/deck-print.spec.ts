import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Export PDF on a dark deck (#1772).
//
// A deck is written for a room with the lights off, and paper is white: a
// browser prints backgrounds only when the reader has turned "Background
// graphics" on, so the deck's own dark page never reached the paper and its
// light text printed on white, near-invisible. The print document is therefore
// always rendered light, and this holds that end to end -- the asset page, the
// button, the print frame the page mounts, and the document inside it at the
// moment the browser is asked to print it.
//
// That moment is the only one there is: the frame tells the page it has
// printed and the page takes it down, so the colours are read from inside the
// frame, in front of `print()`, and posted out. ast-deck is the mock library's
// deck, and it is a dark one (mocks/data/content) for the same reason a real
// one is.

/** The relative luminance of a computed `rgb(...)` colour. */
function luminance(value: string): number {
  const parts = value.match(/\d+(\.\d+)?/g)?.slice(0, 3).map(Number) ?? [];
  const [red, green, blue] = parts;
  if (red === undefined || green === undefined || blue === undefined) return Number.NaN;
  const linear = (channel: number): number => {
    const unit = channel / 255;
    return unit <= 0.04045 ? unit / 12.92 : Math.pow((unit + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * linear(red) + 0.7152 * linear(green) + 0.0722 * linear(blue);
}

test("Export PDF prints a dark deck as a light page with readable ink (#1772)", async ({
  page,
}) => {
  // Every frame reports what it would have put on paper, in front of print().
  await page.addInitScript(() => {
    const native = window.print.bind(window);
    window.print = () => {
      const read = (selector: string, property: string): string => {
        const el = document.querySelector(selector);
        return el ? getComputedStyle(el).getPropertyValue(property) : "";
      };
      window.parent.postMessage(
        {
          mcpRendition: {
            page: read(".pdf-page", "background-color"),
            body: read("body", "background-color"),
            heading: read(".pdf-page h1", "color"),
            prose: read(".pdf-page .muted", "color"),
            panel: read(".pdf-page .panel", "background-color"),
            pages: document.querySelectorAll(".pdf-page").length,
          },
        },
        "*",
      );
      native();
    };
  });

  await authenticate(page);
  await page.goto("/portal/assets/ast-deck");
  await expect(page.getByTestId("viewer-content").locator("iframe")).toBeVisible();

  const reported = page.evaluate(
    () =>
      new Promise<Record<string, string | number>>((resolve) => {
        window.addEventListener("message", (event: MessageEvent) => {
          const data = event.data as { mcpRendition?: Record<string, string | number> };
          if (data?.mcpRendition) resolve(data.mcpRendition);
        });
      }),
  );
  await page.getByRole("button", { name: "Export PDF" }).click();
  const rendition = await reported;

  // The runtime laid the deck out one slide per page before any of this was
  // read, which is what makes these the colours that would be printed.
  expect(Number(rendition["pages"])).toBeGreaterThan(1);
  // The page is white, not the deck's own near-black and not a tint of it.
  expect(rendition["page"]).toBe("rgb(255, 255, 255)");
  expect(rendition["body"]).toBe("rgb(255, 255, 255)");
  // The ink that was written for a dark room reads on white.
  expect(luminance(String(rendition["heading"]))).toBeLessThan(0.18);
  expect(luminance(String(rendition["prose"]))).toBeLessThan(0.18);
  // And a panel the deck filled dark is a pale tint rather than a dark box
  // that would print as nothing under the same setting.
  expect(luminance(String(rendition["panel"]))).toBeGreaterThan(0.6);
});
