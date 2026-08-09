import { type Page } from "@playwright/test";

/**
 * ScreenshotRoute is one capture: where to go, what to click first, and what to
 * wait for. It lives beside the manifest rather than inside it so the manifest
 * and the drawer-state routes it composes can both name the shape.
 */
export interface ScreenshotRoute {
  slug: string;
  path: string;
  category: "user" | "admin";
  tabs?: string[];
  waitFor?: string;
  waitForThumbnails?: number;
  clientNav?: boolean;
  beforeCapture?: (page: Page) => Promise<void>;
}
