import { describe, expect, it } from "vitest";
import { embedPolicy, OPEN_VIDEO_MESSAGE, videoWatchURL, withEmbedPolicy, withVideoCards } from "./videoCards";

describe("withVideoCards", () => {
  it("draws a YouTube player iframe as a card with its poster, keeping its size", () => {
    const out = withVideoCards(
      `<html><body><iframe width="560" height="315" src="https://www.youtube-nocookie.com/embed/M7lc1UVf-VE?rel=0" title="Demo" allowfullscreen></iframe></body></html>`,
    );
    expect(out).not.toContain("<iframe");
    expect(out).toContain(`data-mcp-video="youtube:M7lc1UVf-VE"`);
    expect(out).toContain("https://i.ytimg.com/vi/M7lc1UVf-VE/hqdefault.jpg");
    expect(out).toContain("width:560px;height:315px;");
    expect(out).toContain("Watch on YouTube: Demo");
    expect(out).toContain(OPEN_VIDEO_MESSAGE);
    expect(out.indexOf("<script>")).toBeLessThan(out.indexOf("</body>"));
  });

  it("draws a Vimeo player as a card without a poster", () => {
    const out = withVideoCards(`<iframe src="https://player.vimeo.com/video/76979871"></iframe>`);
    expect(out).toContain(`data-mcp-video="vimeo:76979871"`);
    expect(out).toContain("Watch on Vimeo");
    expect(out).toContain("aspect-ratio:16/9");
  });

  it("leaves Wistia, other iframes and a document without players as written", () => {
    const wistia = `<iframe src="https://fast.wistia.net/embed/iframe/26sk4lmiix"></iframe>`;
    expect(withVideoCards(wistia)).toBe(wistia);
    const words = `<p>Our YouTube channel and Vimeo page</p>`;
    expect(withVideoCards(words)).toBe(words);
    const odd = `<iframe src="https://player.vimeo.com/video/notanumber"></iframe>`;
    expect(withVideoCards(odd)).toBe(odd);
  });

  it("does not let an attribute break out of the card's markup", () => {
    const out = withVideoCards(
      `<iframe class='x" onclick="alert(1)' title='a"b' src="https://www.youtube.com/embed/M7lc1UVf-VE"></iframe>`,
    );
    expect(out).not.toMatch(/\sonclick="/);
  });
});

describe("videoWatchURL", () => {
  it("rebuilds the address from the id", () => {
    expect(videoWatchURL("youtube:M7lc1UVf-VE")).toBe("https://www.youtube.com/watch?v=M7lc1UVf-VE");
    expect(videoWatchURL("vimeo:76979871")).toBe("https://vimeo.com/76979871");
  });

  it("opens nothing else", () => {
    for (const v of [
      "youtube:../x", "vimeo:abc", "javascript:alert(1)", "https://evil.example.com", "youtube:a:b",
      "youtube:", 42, null, { video: "youtube:M7lc1UVf-VE" },
    ]) {
      expect(videoWatchURL(v)).toBeNull();
    }
  });
});

describe("embed policy", () => {
  it("limits frames and media to the page and the hosts", () => {
    expect(embedPolicy(["https://fast.wistia.net", "https://*.cloudfront.net:8443"])).toBe(
      "frame-src 'self' blob: data: https://fast.wistia.net https://*.cloudfront.net:8443; " +
        "media-src 'self' blob: data: https://fast.wistia.net https://*.cloudfront.net:8443",
    );
    expect(embedPolicy([])).toBe("frame-src 'self' blob: data:; media-src 'self' blob: data:");
  });

  it("drops an entry that is not in the stored form", () => {
    expect(embedPolicy(["https://ok.example.com", "x.com; script-src *", "*", "http://a.com"])).toBe(
      "frame-src 'self' blob: data: https://ok.example.com; media-src 'self' blob: data: https://ok.example.com",
    );
  });

  it("puts the policy first in the head, after a doctype, or as the first element", () => {
    const meta = `<meta http-equiv="Content-Security-Policy"`;
    expect(withEmbedPolicy(`<!doctype html><html><head><title>t</title></head></html>`, [])).toMatch(
      new RegExp(`^<!doctype html><html><head>${meta}`),
    );
    expect(withEmbedPolicy(`<html><body>x</body></html>`, [])).toMatch(new RegExp(`^<html><head>${meta}`));
    expect(withEmbedPolicy(`<!DOCTYPE html><p>x</p>`, [])).toMatch(new RegExp(`^<!DOCTYPE html>${meta}`));
    expect(withEmbedPolicy(`<p>x</p>`, [])).toMatch(new RegExp(`^${meta}`));
  });
});
