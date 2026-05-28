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
