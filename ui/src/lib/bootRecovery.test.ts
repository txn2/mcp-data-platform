import fs from "fs";
import path from "path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { reloadOnceForChunkError } from "./chunkReload";
import { bootRecovery } from "../../bootRecoveryPlugin";

// #2495: the inline script index.html carries runs before the entry, so a
// failed entry or preload tag reloads the page once and then says the portal
// is being updated. The script under test is the one the build writes into
// index.html, taken from the plugin that writes it.

const UI = path.resolve(__dirname, "../..");
const STORAGE_KEY = "mcp-portal-chunk-reload-at";

/** index.html as the build transforms it, with base /portal/. */
function transformedIndexHtml(): string {
  const plugin = bootRecovery();
  const configResolved = plugin.configResolved as (c: unknown) => void;
  configResolved({ base: "/portal/", build: { assetsDir: "assets" } });
  const hook = plugin.transformIndexHtml as { handler: (html: string) => string };
  return hook.handler(fs.readFileSync(path.join(UI, "index.html"), "utf8"));
}

function inlineScript(): string {
  const m = /<script id="portal-boot-recovery">([\s\S]*?)<\/script>/.exec(transformedIndexHtml());
  const source = m?.[1];
  if (!source) throw new Error("index.html carries no boot recovery script");
  return source;
}

let listeners: EventListener[] = [];
let reload: ReturnType<typeof vi.fn>;

/** Runs the script with a location whose reload is observable. */
function boot() {
  reload = vi.fn();
  const fakeLocation = { href: window.location.href, origin: window.location.origin, reload };
  const fakeWindow = {
    addEventListener: (type: string, fn: EventListener, capture: boolean) => {
      window.addEventListener(type, fn, capture);
      listeners.push(fn);
    },
  };
  new Function("window", "location", inlineScript())(fakeWindow, fakeLocation);
}

/** Adds an element to the document and fires the error a failed load fires. */
function failLoad(el: HTMLElement) {
  document.head.appendChild(el);
  el.dispatchEvent(new Event("error"));
}

function moduleScript(src: string) {
  const s = document.createElement("script");
  s.type = "module";
  s.src = src;
  return s;
}

function link(rel: string, href: string) {
  const l = document.createElement("link");
  l.rel = rel;
  l.href = href;
  return l;
}

const message = () => document.querySelector('#root [role="alert"]');

beforeEach(() => {
  document.head.innerHTML = "";
  document.body.innerHTML = '<div id="root"></div>';
  vi.spyOn(Date, "now").mockReturnValue(1_000_000);
});

afterEach(() => {
  for (const fn of listeners) window.removeEventListener("error", fn, true);
  listeners = [];
  vi.restoreAllMocks();
});

describe("boot recovery in index.html (#2495)", () => {
  it("is written after the charset meta and above the module entry", () => {
    const html = transformedIndexHtml();
    const charset = html.indexOf('<meta charset="UTF-8" />');
    const script = html.indexOf('<script id="portal-boot-recovery">');
    const entry = html.indexOf('<script type="module"');
    expect(charset).toBeGreaterThanOrEqual(0);
    expect(script).toBeGreaterThan(charset);
    expect(entry).toBeGreaterThan(script);
    expect(inlineScript()).toContain('var ASSETS = "/portal/assets/";');
  });

  it("reloads once when the entry script fails and records when", () => {
    boot();
    failLoad(moduleScript("/portal/assets/index-abc.js"));
    expect(reload).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBe("1000000");
    expect(message()).toBeNull();
  });

  it("reloads when a modulepreload or the stylesheet fails", () => {
    boot();
    failLoad(link("modulepreload", "/portal/assets/client-abc.js"));
    expect(reload).toHaveBeenCalledTimes(1);

    sessionStorage.clear();
    boot();
    failLoad(link("stylesheet", "/portal/assets/index-abc.css"));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("handles one failure per page even when several tags fail", () => {
    boot();
    failLoad(link("modulepreload", "/portal/assets/client-abc.js"));
    failLoad(link("modulepreload", "/portal/assets/react-abc.js"));
    failLoad(moduleScript("/portal/assets/index-abc.js"));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("does not reload a second time inside the window and shows the message", () => {
    sessionStorage.setItem(STORAGE_KEY, String(1_000_000 - 29_000));
    boot();
    failLoad(moduleScript("/portal/assets/index-abc.js"));
    expect(reload).not.toHaveBeenCalled();
    expect(message()?.textContent).toContain("The portal is being updated. Reload in a moment.");
    const button = document.querySelector<HTMLButtonElement>("#root button");
    expect(button?.textContent).toBe("Reload");
    button?.click();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("reloads again once the window has passed", () => {
    sessionStorage.setItem(STORAGE_KEY, String(1_000_000 - 31_000));
    boot();
    failLoad(moduleScript("/portal/assets/index-abc.js"));
    expect(reload).toHaveBeenCalledTimes(1);
    expect(message()).toBeNull();
  });

  it("shares the window with the lazy chunk reload (#2139)", () => {
    expect(reloadOnceForChunkError(new Error("error loading dynamically imported module"), 1_000_000, () => {})).toBe(true);
    boot();
    failLoad(moduleScript("/portal/assets/index-abc.js"));
    expect(reload).not.toHaveBeenCalled();
    expect(message()).not.toBeNull();
  });

  it("ignores an image, a script outside the assets path and another origin", () => {
    boot();
    const img = document.createElement("img");
    img.src = "/portal/assets/logo.png";
    failLoad(img);
    failLoad(moduleScript("/portal/vendor/maplibre/maplibre-gl.js"));
    failLoad(moduleScript("https://cdn.example.com/portal/assets/index-abc.js"));
    const classic = document.createElement("script");
    classic.src = "/portal/assets/index-abc.js";
    failLoad(classic);
    failLoad(link("icon", "/portal/assets/favicon.ico"));
    expect(reload).not.toHaveBeenCalled();
    expect(message()).toBeNull();
    expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it("stands aside once the app has drawn", () => {
    document.getElementById("root")!.innerHTML = "<main>portal</main>";
    boot();
    failLoad(link("modulepreload", "/portal/assets/route-abc.js"));
    expect(reload).not.toHaveBeenCalled();
    expect(document.querySelector("#root main")).not.toBeNull();
  });

  it("still reloads when storage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    boot();
    failLoad(moduleScript("/portal/assets/index-abc.js"));
    expect(reload).toHaveBeenCalledTimes(1);
  });
});
