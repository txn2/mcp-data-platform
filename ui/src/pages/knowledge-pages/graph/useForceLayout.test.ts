import { describe, it, expect } from "vitest";
import { clampAxis } from "./useForceLayout";

/**
 * A d3 force runs BEFORE the tick integrates `x += vx * velocityDecay`, so a
 * bounds force that clamps the current position alone has its work undone in the
 * same tick and crowded graphs still throw their outermost nodes off the canvas.
 * clampAxis is the arithmetic that fixes that, so it is pinned here directly.
 */
describe("clampAxis", () => {
  const MIN = 70;
  const MAX = 530;

  it("leaves a node that will land inside untouched", () => {
    expect(clampAxis(300, 10, MIN, MAX)).toEqual({ pos: 300, vel: 10 });
  });

  it("clamps on where the node will LAND, not where it is", () => {
    // Inside the bounds now, but a velocity of -200 decays to -120 and would
    // carry it to -20 next tick.
    const out = clampAxis(100, -200, MIN, MAX);
    expect(out.pos).toBe(MIN);
    expect(out.vel).toBe(0);
  });

  it("stops the outward velocity so the clamp is not immediately undone", () => {
    const out = clampAxis(529, 100, MIN, MAX);
    expect(out.pos + out.vel * 0.6).toBeLessThanOrEqual(MAX);
  });

  it("keeps a node inside after the integration step, on both edges", () => {
    for (const [pos, vel] of [
      [MIN, -50],
      [MAX, 50],
      [10, 0],
      [900, 0],
    ] as const) {
      const out = clampAxis(pos, vel, MIN, MAX);
      const landing = out.pos + out.vel * 0.6;
      expect(landing).toBeGreaterThanOrEqual(MIN);
      expect(landing).toBeLessThanOrEqual(MAX);
    }
  });
});
