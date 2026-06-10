import { type Page } from "@playwright/test";

/**
 * Apply the target theme BEFORE navigating so the page loads already in it.
 *
 * The app's theme store (src/stores/theme.ts) reads localStorage
 * ("mcp-portal-theme") and toggles the `.dark` class at module load, before
 * React renders. Components that read the theme only at mount — notably the
 * CodeMirror MarkdownEditor, which checks `documentElement.classList` once and
 * does not reactively re-theme — therefore initialize in the correct theme.
 *
 * The previous approach (load light, then click the header toggle) left those
 * editors stuck on the light theme: a white CodeMirror box in dark mode. It
 * also broke when an open drawer overlay covered the toggle. Setting the theme
 * up front, and emulating the matching color scheme, avoids both problems.
 *
 * localStorage and the emulated color scheme persist across same-origin
 * navigations, so this only needs to run before each goto.
 */
export async function applyTheme(
  page: Page,
  theme: "light" | "dark",
): Promise<void> {
  await page.emulateMedia({ colorScheme: theme });
  await page
    .evaluate((t) => {
      try {
        localStorage.setItem("mcp-portal-theme", t);
      } catch {
        // localStorage unavailable
      }
      document.documentElement.classList.toggle("dark", t === "dark");
    }, theme)
    .catch(() => {
      // Page not navigated yet (first run); the emulated scheme + the next
      // navigation's localStorage read still apply the theme on load.
    });
}
