import { describe, expect, it } from "vitest";
import { cleanGraphvizSvg, dotEngine } from "./graphvizSvg";

const SVG_NS = 'xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"';

describe("dotEngine", () => {
  it("reads the engine a source names, and defaults to dot", () => {
    expect(dotEngine("digraph { a -> b }")).toBe("dot");
    expect(dotEngine('graph { layout=neato; a -- b }')).toBe("neato");
    expect(dotEngine('graph { layout="CIRCO" }')).toBe("circo");
    expect(dotEngine("graph { layout=madeup }")).toBe("dot");
  });
});

describe("cleanGraphvizSvg", () => {
  it("unwraps links, removes what loads or runs, and keeps the drawing", () => {
    const svg = cleanGraphvizSvg(`<svg ${SVG_NS} viewBox="0 0 10 10" onload="alert(1)">
      <g class="graph"><polygon fill="white" stroke="none" points="0,0"/>
        <a xlink:href="javascript:alert(1)" xlink:title="t" target="_blank">
          <g class="node"><title>a</title><ellipse fill="none" stroke="black" onclick="x()"/>
            <text fill="black">a</text><text font-family="Times,serif">b</text></g>
        </a>
        <image xlink:href="https://example.com/x.png"/>
        <script>alert(1)</script>
        <foreignObject><div>x</div></foreignObject>
        <use href="https://example.com/s.svg#x"/>
        <polygon fill="#ff0000" stroke="#00ff00" points="1,1"/>
        <path fill="url(https://example.com/p.svg#g)" style="fill:url('https://example.com/p')" d="M0 0"/>
        <path fill="url(#l_0)" d="M1 1"/>
      </g></svg>`);
    const markup = new XMLSerializer().serializeToString(svg);
    expect(markup).not.toMatch(/javascript:|onload|onclick|<a\b|<image|<script|foreignObject|<use|example\.com|target=/i);
    expect(svg.querySelector("ellipse")?.getAttribute("stroke")).toBe("currentColor");
    expect(svg.querySelector("text")?.getAttribute("fill")).toBe("currentColor");
    const unfilled = svg.querySelectorAll("text")[1];
    expect(unfilled?.getAttribute("fill")).toBe("currentColor");
    expect(unfilled?.getAttribute("font-family")).toMatch(/sans-serif/);
    expect(svg.querySelector("title")?.textContent).toBe("a");
    // A gradient Graphviz defines in the drawing is kept.
    expect(svg.querySelectorAll("path")[1]?.getAttribute("fill")).toBe("url(#l_0)");
    // The white page is removed; a color the source chose stays.
    expect(svg.querySelectorAll("polygon")).toHaveLength(1);
    expect(svg.querySelector("polygon")?.getAttribute("fill")).toBe("#ff0000");
  });

  it("refuses output that is not an SVG", () => {
    expect(() => cleanGraphvizSvg("<html/>")).toThrow(/did not produce an SVG/);
    expect(() => cleanGraphvizSvg("not xml")).toThrow(/did not produce an SVG/);
  });
});
