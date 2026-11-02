import { describe, expect, it } from "vitest";
import { strToU8, zipSync } from "fflate";
import { inlineOnly, parseAnimation, readDotLottie } from "./lottieSource";

const ANIM = { v: "5.7.4", fr: 30, ip: 0, op: 60, w: 100, h: 100, layers: [] };

describe("lottieSource", () => {
  it("parses an animation and refuses what is not one", () => {
    expect(parseAnimation(JSON.stringify(ANIM)).op).toBe(60);
    expect(() => parseAnimation(`{"layers":[]}`)).toThrow(/fr, ip, op and layers/);
    expect(() => parseAnimation("[")).toThrow();
  });

  it("keeps only inline images and drops font paths", () => {
    const out = inlineOnly({
      ...ANIM,
      assets: [
        { id: "a", p: "data:image/png;base64,AA", e: 1 },
        { id: "b", u: "https://cdn.example.com/", p: "b.png", e: 0 },
        { id: "comp", layers: [] },
      ],
      fonts: { list: [{ fName: "X", fPath: "https://fonts.example.com/x.css" }] },
    });
    expect(out.assets![0]!.p).toBe("data:image/png;base64,AA");
    expect(out.assets![1]).toMatchObject({ p: "", u: "", e: 1 });
    expect(out.assets![2]).toEqual({ id: "comp", layers: [] });
    expect(out.fonts!.list![0]!.fPath).toBe("");
  });

  it("reads a dotLottie archive with its images inlined", () => {
    const zip = zipSync({
      "manifest.json": strToU8(JSON.stringify({ animations: [{ id: "intro" }] })),
      "animations/intro.json": strToU8(JSON.stringify({ ...ANIM, assets: [{ id: "img", u: "/images/", p: "logo.png", e: 0 }] })),
      "images/logo.png": new Uint8Array([137, 80, 78, 71]),
    });
    const anim = readDotLottie(zip);
    expect(anim.op).toBe(60);
    expect(anim.assets![0]!.p).toBe("data:image/png;base64,iVBORw==");
  });

  it("refuses an archive without a manifest or its animation", () => {
    expect(() => readDotLottie(zipSync({ "x.json": strToU8("{}") }))).toThrow(/no manifest/);
    expect(() => readDotLottie(zipSync({ "manifest.json": strToU8(`{"animations":[{"id":"a"}]}`) }))).toThrow(/no animation a/);
    expect(() => readDotLottie(zipSync({ "manifest.json": strToU8(`{}`) }))).toThrow(/names no animation/);
  });
});
