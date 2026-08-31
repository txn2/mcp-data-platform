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
  /**
   * Run after the screenshot, to put back any state the capture changed. The
   * whole run shares one page and one origin, so a route that toggles a
   * persisted control (a collapsed section intro) leaves it that way for every
   * later capture of that section unless it restores it here.
   */
  afterCapture?: (page: Page) => Promise<void>;
}
