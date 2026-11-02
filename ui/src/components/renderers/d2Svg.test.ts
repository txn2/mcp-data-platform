import { describe, expect, it } from "vitest";
import { cleanD2Svg, drawingURL } from "./d2Svg";

const ORIGIN = "https://portal.example.com";

function svg(body: string): string {
  return `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 10 10">${body}</svg>`;
}

describe("cleanD2Svg", () => {
  it("keeps D2's stylesheet and drawing, without addresses outside it", () => {
    const out = cleanD2Svg(
      svg(`<style>.d2-1 .fill-N1{fill:#0A0F25} @font-face{src:url(data:font/woff;base64,AA)} .x{background:url(https://evil.example.com/a.png)}</style>
        <rect class="fill-N1" onclick="alert(1)" fill="url(https://evil.example.com/p)"/><text>web</text>`),
      ORIGIN,
    );
    const style = out.querySelector("style")!.textContent!;
    expect(style).toContain(".fill-N1");
    expect(style).toContain("url(data:font/woff");
    expect(style).not.toContain("evil.example.com");
    const rect = out.querySelector("rect")!;
    expect(rect.getAttribute("onclick")).toBeNull();
    expect(rect.getAttribute("fill")).toBeNull();
    expect(out.textContent).toContain("web");
  });

  it("keeps an icon only from the reference route, pointed at the page's own origin", () => {
    const out = cleanD2Svg(
      svg(`<image href="https://other-host.example.com/portal/refs/asset1/tok1"/>
           <image href="https://icons.terrastruct.com/aws.svg"/>
           <image href="/portal/refs/asset1/tok2?x=1"/>
           <image xlink:href="javascript:alert(1)"/>`),
      ORIGIN,
    );
    const images = out.querySelectorAll("image");
    expect(images).toHaveLength(1);
    expect(images[0]!.getAttribute("href")).toBe(`${ORIGIN}/portal/refs/asset1/tok1`);
  });

  it("unwraps links and removes what runs", () => {
    const out = cleanD2Svg(svg(`<a href="https://evil.example.com"><text>go</text></a><script>alert(1)</script><use href="#x"/>`), ORIGIN);
    expect(out.querySelector("a")).toBeNull();
    expect(out.querySelector("script")).toBeNull();
    expect(out.querySelector("use")).toBeNull();
    expect(out.textContent).toContain("go");
  });

  it("removes a stylesheet a label's HTML carries, keeping D2's own", () => {
    const out = cleanD2Svg(
      svg(`<style>.d2-1 .shape{stroke:#000}</style><foreignObject width="100" height="20"><div xmlns="http://www.w3.org/1999/xhtml"><style>body{display:none}</style><p>label</p></div></foreignObject>`),
      ORIGIN,
    );
    const styles = Array.from(out.querySelectorAll("style")).map((s) => s.textContent);
    expect(styles).toEqual([".d2-1 .shape{stroke:#000}"]);
    expect(out.textContent).toContain("label");
  });

  it("keeps a markdown label's text without its script or handlers", () => {
    const out = cleanD2Svg(
      svg(`<foreignObject width="100" height="20"><div xmlns="http://www.w3.org/1999/xhtml"><p onclick="alert(1)">hello <b>world</b></p><script>alert(2)</script><img src="https://evil.example.com/x.png"></div></foreignObject>`),
      ORIGIN,
    );
    const html = out.querySelector("foreignObject")!.innerHTML;
    expect(out.textContent).toContain("hello world");
    expect(html).not.toMatch(/onclick|<script|evil\.example\.com/);
  });

  it("refuses what is not an SVG", () => {
    expect(() => cleanD2Svg("<p>no</p>", ORIGIN)).toThrow();
  });

  it("asks the content route for the drawing in the page's scheme", () => {
    expect(drawingURL("/api/v1/portal/assets/a1/content", false)).toBe("/api/v1/portal/assets/a1/content?render=svg");
    expect(drawingURL("/portal/view/t/content?v=2", true)).toBe("/portal/view/t/content?v=2&render=svg&theme=dark");
  });
});
