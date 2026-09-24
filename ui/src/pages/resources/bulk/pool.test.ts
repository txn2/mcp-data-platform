import { describe, expect, it } from "vitest";
import { runPool } from "./pool";

describe("runPool", () => {
  it("never has more than the limit in flight and attempts every item", async () => {
    let inFlight = 0;
    let peak = 0;
    const done: number[] = [];
    await runPool([1, 2, 3, 4, 5, 6, 7, 8, 9], 4, async (n) => {
      inFlight += 1;
      peak = Math.max(peak, inFlight);
      await new Promise((r) => setTimeout(r, 1));
      inFlight -= 1;
      done.push(n);
    });
    expect(peak).toBe(4);
    expect(done.sort((a, b) => a - b)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9]);
  });

  it("keeps going past a rejected item", async () => {
    const done: number[] = [];
    await runPool([1, 2, 3], 1, async (n) => {
      if (n === 2) throw new Error("refused");
      done.push(n);
    });
    expect(done).toEqual([1, 3]);
  });

  it("starts nothing new once asked to stop", async () => {
    const started: number[] = [];
    let stop = false;
    await runPool(
      [1, 2, 3, 4],
      1,
      async (n) => {
        started.push(n);
        if (n === 2) stop = true;
      },
      () => stop,
    );
    expect(started).toEqual([1, 2]);
  });

  it("resolves at once for an empty list", async () => {
    let called = false;
    await runPool([], 4, async () => {
      called = true;
    });
    expect(called).toBe(false);
  });
});
