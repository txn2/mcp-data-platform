import "@testing-library/jest-dom";

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
