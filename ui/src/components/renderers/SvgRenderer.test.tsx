import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render } from "@testing-library/react";
import { SvgRenderer } from "./SvgRenderer";

afterEach(cleanup);

// #1991: an SVG is drawn on the checkerboard the raster viewer uses, so white
// and black artwork are visible in both schemes, not on a solid card.
describe("SvgRenderer", () => {
  it("draws the sanitized SVG on the checkerboard", () => {
    const { container } = render(
      <SvgRenderer content={'<svg xmlns="http://www.w3.org/2000/svg"><rect fill="#fff" width="10" height="10"/><script>x()</script></svg>'} />,
    );
    const backing = container.firstElementChild!;
    expect(backing).toHaveClass("checkerboard");
    expect(backing).not.toHaveClass("bg-card");
    expect(backing.querySelector("rect")).not.toBeNull();
    expect(backing.querySelector("script")).toBeNull();
  });
});
