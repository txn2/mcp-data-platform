import "@testing-library/jest-dom";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

// A Radix focus scope schedules its "focus moved away on unmount" event on a
// zero-delay timer *during* unmount, and builds the event when the timer fires.
// Left pending when a test file finishes, it fires against a torn-down jsdom
// realm and vitest reports an unhandled "parameter 1 is not of type 'Event'"
// against whichever file happened to render a dialog last. Unmount and let
// those timers run while the realm the elements belong to is still alive.
afterEach(async () => {
  cleanup();
  await new Promise((resolve) => setTimeout(resolve, 0));
});

// jsdom has no ResizeObserver, which recharts' ResponsiveContainer
// requires. Stub it so components that render charts can be tested.
class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
globalThis.ResizeObserver =
  globalThis.ResizeObserver ?? (ResizeObserverStub as unknown as typeof ResizeObserver);

// Radix listboxes (ui/select) capture the pointer while a menu is open and
// scroll the active item into view. jsdom implements neither, so opening a
// Select in a test throws before the options render.
HTMLElement.prototype.hasPointerCapture ??= () => false;
HTMLElement.prototype.setPointerCapture ??= () => {};
HTMLElement.prototype.releasePointerCapture ??= () => {};
HTMLElement.prototype.scrollIntoView ??= () => {};

// jsdom implements neither idle callback, so deferred work (thumbnail capture,
// see lib/idle) would fall back to its one-second timer and every test that
// waits for it would sit out that second. Browsers that matter schedule the
// callback promptly, so the stub does too, and the tests exercise the same
// path production takes.
interface IdleGlobal {
  requestIdleCallback?: (cb: () => void) => number;
  cancelIdleCallback?: (handle: number) => void;
}
const idleGlobal = globalThis as unknown as IdleGlobal;
idleGlobal.requestIdleCallback ??= (cb: () => void) => setTimeout(cb, 0) as unknown as number;
idleGlobal.cancelIdleCallback ??= (handle: number) =>
  clearTimeout(handle as unknown as ReturnType<typeof setTimeout>);
