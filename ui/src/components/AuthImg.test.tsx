import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";

// In cookie-auth mode useAuthSrc returns the URL as-is; mock it so the test
// focuses on the <img> attributes AuthImg sets.
vi.mock("@/hooks/useAuthSrc", () => ({ useAuthSrc: (url: string | undefined) => url }));

import { AuthImg } from "./AuthImg";

describe("AuthImg", () => {
  it("defaults to lazy loading and async decoding", () => {
    const { container } = render(<AuthImg src="/x.png" alt="" />);
    const img = container.querySelector("img")!;
    expect(img.getAttribute("loading")).toBe("lazy");
    expect(img.getAttribute("decoding")).toBe("async");
  });

  it("lets callers override the loading attribute", () => {
    const { container } = render(<AuthImg src="/x.png" alt="" loading="eager" />);
    expect(container.querySelector("img")!.getAttribute("loading")).toBe("eager");
  });
});
