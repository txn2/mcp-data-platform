import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/react";

const { uploadThumbnail, downscaleImage } = vi.hoisted(() => ({
  uploadThumbnail: vi.fn(),
  downscaleImage: vi.fn(),
}));
vi.mock("@/lib/thumbnail", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/thumbnail")>()),
  uploadThumbnail,
  downscaleImage,
}));

import { ImageCapture } from "./ImageCapture";

// A raster image is not rendered, it is resized (#1554). The tile used to be
// the original object scaled down by CSS, so a gallery pulled every file at
// full size to draw postage stamps and anything past a cutoff drew nothing.

const target = { kind: "resource" as const, id: "res-1" };

beforeEach(() => {
  uploadThumbnail.mockReset();
  uploadThumbnail.mockResolvedValue(undefined);
  downscaleImage.mockReset();
  downscaleImage.mockResolvedValue(new Blob(["png"]));
});

afterEach(cleanup);

describe("capturing a raster image", () => {
  it("downscales the resource's own bytes and uploads the result", async () => {
    const onCaptured = vi.fn();
    render(
      <ImageCapture target={target} contentType="image/png" onCaptured={onCaptured} />,
    );

    await waitFor(() => expect(onCaptured).toHaveBeenCalledTimes(1));
    // Read from the resource's content route, not from the text `content` prop:
    // an image read as text is corrupt before it gets here.
    expect(downscaleImage).toHaveBeenCalledWith("/api/v1/resources/res-1/content", "image/png");
    expect(uploadThumbnail).toHaveBeenCalledWith(target, expect.any(Blob), "light", undefined);
  });

  // An image carries its own colours, so one capture serves both modes.
  it("captures one variant, not a light and dark pair", async () => {
    const onCaptured = vi.fn();
    render(<ImageCapture target={target} contentType="image/jpeg" onCaptured={onCaptured} />);

    await waitFor(() => expect(onCaptured).toHaveBeenCalled());
    expect(uploadThumbnail).toHaveBeenCalledTimes(1);
  });

  it("reports a failure rather than a capture when the image cannot be read", async () => {
    downscaleImage.mockRejectedValue(new Error("could not load"));
    const onCaptured = vi.fn();
    const onFailed = vi.fn();

    render(
      <ImageCapture
        target={target}
        contentType="image/png"
        onCaptured={onCaptured}
        onFailed={onFailed}
      />,
    );

    await waitFor(() => expect(onFailed).toHaveBeenCalledTimes(1));
    expect(onCaptured).not.toHaveBeenCalled();
  });

  it("reports a failure when the upload is refused", async () => {
    uploadThumbnail.mockRejectedValue(new Error("nope"));
    const onFailed = vi.fn();

    render(<ImageCapture target={target} contentType="image/png" onFailed={onFailed} />);

    await waitFor(() => expect(onFailed).toHaveBeenCalledTimes(1));
  });

  // The effect guards itself: a re-render must not upload the same image twice.
  it("captures once however often it re-renders", async () => {
    const onCaptured = vi.fn();
    const { rerender } = render(
      <ImageCapture target={target} contentType="image/png" onCaptured={onCaptured} />,
    );
    await waitFor(() => expect(onCaptured).toHaveBeenCalled());

    rerender(<ImageCapture target={target} contentType="image/png" onCaptured={onCaptured} />);
    expect(uploadThumbnail).toHaveBeenCalledTimes(1);
  });
});
