/**
 * Moving an open portal page to a new build (#2727).
 *
 * A page opened before an upgrade kept running the build it loaded for as long
 * as it stayed open, so a fix that shipped looked as if it never landed. The
 * index.html the platform serves names the build that served it (a meta
 * element), and every answer names the build of the replica that answered it
 * (the X-Platform-Build header). When an answer names a build this tab has not
 * run, the page reloads at the reader's next navigation, or, while the reader
 * is in the middle of something, shows a notice with Reload.
 *
 * Two rules keep a rolling update from looping. Only a build new to the tab
 * counts: every build the tab has run is remembered, so an answer from a
 * replica still on the old build, after the page moved on, changes nothing.
 * And a reload happens at most once per new build: one that lands back on the
 * old build (its index.html came from an old replica) leaves the notice, never
 * a second automatic reload.
 */

import { hasUnsavedChanges } from "@/lib/navigationGuard";

/** The response header naming the build that answered. */
export const BUILD_HEADER = "X-Platform-Build";
/** The meta element naming the build the page loaded. */
export const BUILD_META = "mcp-platform-build";

const SEEN_KEY = "mcp-portal-builds-seen";
const RELOADED_KEY = "mcp-portal-builds-reloaded";

/** The part of Storage the watch uses; sessionStorage in the page. */
export type BuildStore = Pick<Storage, "getItem" | "setItem">;

function sessionStore(): BuildStore | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

function readList(store: BuildStore | null, key: string): string[] {
  try {
    const v = JSON.parse(store?.getItem(key) ?? "[]");
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}

function writeList(store: BuildStore | null, key: string, list: string[]) {
  try {
    store?.setItem(key, JSON.stringify(list.slice(-20)));
  } catch {
    // Without storage a tab forgets the builds it ran when it reloads, so an
    // old replica's answer could look new once; the reload guard below still
    // holds within the page.
  }
}

type Listener = () => void;

/** BuildWatch is one page's view of its build and the newest it has heard of. */
export class BuildWatch {
  readonly pageBuild: string;
  private pending: string | null = null;
  private listeners = new Set<Listener>();
  private reloadedThisPage = new Set<string>();

  private store: BuildStore | null;

  constructor(pageBuild: string, store: BuildStore | null = sessionStore()) {
    this.pageBuild = pageBuild;
    this.store = store;
    if (pageBuild) {
      const seen = readList(store, SEEN_KEY);
      if (!seen.includes(pageBuild)) writeList(store, SEEN_KEY, [...seen, pageBuild]);
    }
  }

  /** observe reads one answer's build. */
  observe(build: string | null) {
    if (!this.pageBuild || !build || build === this.pageBuild) return;
    if (readList(this.store, SEEN_KEY).includes(build)) return;
    if (this.pending === build) return;
    this.pending = build;
    this.listeners.forEach((l) => l());
  }

  /** The new build the page has heard of and not moved to, or null. */
  pendingBuild(): string | null {
    return this.pending;
  }

  /** Whether the page already reloaded once for the pending build and came back without it. */
  alreadyTried(): boolean {
    return this.pending !== null && (this.reloadedThisPage.has(this.pending) || readList(this.store, RELOADED_KEY).includes(this.pending));
  }

  /**
   * reloadOnNavigate reports whether a navigation should load its destination
   * in full, and records the attempt so it happens once per new build.
   */
  reloadOnNavigate(): boolean {
    if (!this.pending || this.alreadyTried()) return false;
    this.reloadedThisPage.add(this.pending);
    writeList(this.store, RELOADED_KEY, [...readList(this.store, RELOADED_KEY), this.pending]);
    return true;
  }

  subscribe(l: Listener): () => void {
    this.listeners.add(l);
    return () => this.listeners.delete(l);
  }
}

/** readPageBuild is the build the served index.html names, or "" (a dev server, a test). */
export function readPageBuild(doc: Document = document): string {
  return doc.querySelector<HTMLMetaElement>(`meta[name="${BUILD_META}"]`)?.content ?? "";
}

let watch = new BuildWatch("");

/** currentBuildWatch is the page's watch, the one installBuildWatch created. */
export function currentBuildWatch(): BuildWatch {
  return watch;
}

/**
 * installBuildWatch reads the page's build and observes the build header on
 * every same-origin answer the page receives, whichever client fetched it.
 */
export function installBuildWatch(win: Window & typeof globalThis = window): BuildWatch {
  watch = new BuildWatch(readPageBuild(win.document));
  if (!watch.pageBuild) return watch;
  const original = win.fetch.bind(win);
  win.fetch = async (...args: Parameters<typeof fetch>) => {
    const res = await original(...args);
    try {
      if (new URL(res.url, win.location.href).origin === win.location.origin) {
        watch.observe(res.headers.get(BUILD_HEADER));
      }
    } catch {
      // An answer with no readable URL names no build.
    }
    return res;
  };
  return watch;
}

/**
 * readerIsBusy reports whether reloading now would cost the reader something:
 * a form with unsaved changes, or an open dialog (an upload, a confirmation).
 */
export function readerIsBusy(doc: Document = document): boolean {
  return hasUnsavedChanges() || doc.querySelector('[role="dialog"], [role="alertdialog"]') !== null;
}
