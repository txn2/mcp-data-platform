import { describe, it, expect, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";

// The resolve is the hook's job and is tested there; this stands in for it so
// the test focuses on what AuthImg does with the answer. A URL of "/fails.png"
// stands for a source the session was refused.
vi.mock("@/hooks/useAuthSrc", () => ({
  useAuthSrc: (url: string | undefined) =>
    url === "/fails.png" ? { failed: true } : { src: url, failed: false },
}));

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

  // On an API-key session the bytes are fetched ahead of the element, so a
  // refusal produces no element and therefore no error event: a caller with a
  // fallback to draw -- a card's content-type icon, the thumbnail panel's "No
  // thumbnail stored" -- would wait on one that was never coming (#1568).
  it("reports a source it could not resolve, where there is no element to error", async () => {
    const onLoadFailed = vi.fn();
    const { container } = render(
      <AuthImg src="/fails.png" alt="" onLoadFailed={onLoadFailed} />,
    );
    await waitFor(() => expect(onLoadFailed).toHaveBeenCalled());
    expect(container.querySelector("img")).toBeNull();
  });

  it("says nothing about a source that resolved", async () => {
    const onLoadFailed = vi.fn();
    render(<AuthImg src="/x.png" alt="" onLoadFailed={onLoadFailed} />);
    await waitFor(() => expect(onLoadFailed).not.toHaveBeenCalled());
  });
});
