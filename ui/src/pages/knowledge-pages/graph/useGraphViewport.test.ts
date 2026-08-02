import { describe, it, expect } from "vitest";
import { IDENTITY, clampScale, toGraphPoint, wheelZoomFactor, zoomAbout } from "./useGraphViewport";

describe("clampScale", () => {
  it("keeps zoom inside the legible range", () => {
    expect(clampScale(0.01)).toBe(0.25);
    expect(clampScale(100)).toBe(4);
    expect(clampScale(1.5)).toBe(1.5);
  });
});

describe("zoomAbout", () => {
  it("keeps the point under the cursor fixed while scaling", () => {
    const t = zoomAbout(IDENTITY, 2, 300, 200);
    expect(t.k).toBe(2);
    // The graph point that was under (300,200) must still be under it.
    const before = toGraphPoint(IDENTITY, 300, 200);
    const after = toGraphPoint(t, 300, 200);
    expect(after.x).toBeCloseTo(before.x);
    expect(after.y).toBeCloseTo(before.y);
  });

  it("returns the same transform when already at a bound", () => {
    const maxed = zoomAbout(IDENTITY, 100, 0, 0);
    expect(zoomAbout(maxed, 2, 10, 10)).toBe(maxed);
  });

  it("composes with a pan offset", () => {
    const panned = { k: 1, x: 50, y: -20 };
    const t = zoomAbout(panned, 2, 100, 100);
    const before = toGraphPoint(panned, 100, 100);
    const after = toGraphPoint(t, 100, 100);
    expect(after.x).toBeCloseTo(before.x);
    expect(after.y).toBeCloseTo(before.y);
  });
});

describe("toGraphPoint", () => {
  it("inverts the pan and zoom applied to the graph layer", () => {
    const t = { k: 2, x: 40, y: 10 };
    expect(toGraphPoint(t, 140, 110)).toEqual({ x: 50, y: 50 });
  });
});

describe("wheelZoomFactor", () => {
  it("scales with how far the wheel actually moved", () => {
    // A fixed step per event is what made trackpad zoom uncontrollable: one
    // gesture delivers dozens of events, so each must contribute in proportion.
    const small = wheelZoomFactor(-4, 0);
    const large = wheelZoomFactor(-120, 0);
    expect(small).toBeGreaterThan(1);
    expect(large).toBeGreaterThan(small);
    // A typical trackpad tick must be a nudge, not a jump.
    expect(small).toBeLessThan(1.02);
  });

  it("zooms in on negative delta and out on positive", () => {
    expect(wheelZoomFactor(-100, 0)).toBeGreaterThan(1);
    expect(wheelZoomFactor(100, 0)).toBeLessThan(1);
    expect(wheelZoomFactor(0, 0)).toBe(1);
  });

  it("is symmetric, so a gesture and its reverse cancel out", () => {
    expect(wheelZoomFactor(-80, 0) * wheelZoomFactor(80, 0)).toBeCloseTo(1);
  });

  it("treats line and page deltas as much larger units than pixels", () => {
    // A mouse wheel reporting 3 lines must move the view like a real notch, not
    // like three pixels of trackpad travel.
    expect(wheelZoomFactor(-3, 1)).toBeGreaterThan(wheelZoomFactor(-3, 0));
    expect(wheelZoomFactor(-1, 2)).toBeGreaterThan(wheelZoomFactor(-1, 1));
  });

  it("falls back to pixel units for an unknown deltaMode", () => {
    expect(wheelZoomFactor(-100, 99)).toBe(wheelZoomFactor(-100, 0));
  });

  it("keeps a whole trackpad gesture inside a sane range", () => {
    // 30 events of -10px, which is one comfortable two-finger swipe. The old
    // fixed 1.2x per event compounded this to 1.2^30, about 237x.
    let k = 1;
    for (let i = 0; i < 30; i++) k *= wheelZoomFactor(-10, 0);
    expect(k).toBeGreaterThan(1.2);
    expect(k).toBeLessThan(2);
  });
});
