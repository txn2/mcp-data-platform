import { type Page } from "@playwright/test";

let currentTheme: "light" | "dark" | null = null;

export async function setTheme(
  page: Page,
  theme: "light" | "dark",
): Promise<void> {
  if (currentTheme === theme) return;

  const label = theme === "dark" ? "Dark" : "Light";
  const btn = page.locator(`button[title="${label}"]`);

  const visible = await btn.isVisible().catch(() => false);
  if (visible) {
    await btn.click({ timeout: 3_000 });
  } else {
    await page.evaluate((t) => {
      localStorage.setItem("mcp-portal-theme", t);
      document.documentElement.classList.toggle("dark", t === "dark");
    }, theme);
  }

  await page.waitForTimeout(200);
  currentTheme = theme;
}

export function resetThemeTracking(): void {
  currentTheme = null;
}
