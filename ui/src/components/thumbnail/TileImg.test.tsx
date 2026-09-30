import { afterEach, describe, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { TileImg } from "./TileImg";

afterEach(cleanup);

// #1991: a tile is shown on the checkerboard once it has loaded, so a
// transparent SVG or image tile shows its artwork on the board while an opaque
// tile covers it; a tile still loading shows no board.
describe("TileImg", () => {
  it("puts the checkerboard behind a tile once it has loaded", () => {
    const loaded: string[] = [];
    render(<TileImg src="/t/a.png" alt="tile" className="object-cover" onLoad={() => loaded.push("a")} />);
    const img = screen.getByAltText("tile");
    expect(img).not.toHaveClass("checkerboard");
    fireEvent.load(img);
    expect(img).toHaveClass("checkerboard", "object-cover");
    expect(loaded).toEqual(["a"]);
  });

  it("waits for the picture to decode before putting the board on", async () => {
    let decoded: () => void = () => {};
    render(<TileImg src="/t/a.png" alt="tile" />);
    const img = screen.getByAltText("tile") as HTMLImageElement;
    img.decode = () => new Promise<void>((resolve) => (decoded = resolve));
    fireEvent.load(img);
    expect(img).not.toHaveClass("checkerboard");
    decoded();
    await waitFor(() => expect(img).toHaveClass("checkerboard"));
  });

  it("takes the board away while a new tile loads in the same element", () => {
    const { rerender } = render(<TileImg src="/t/a.png" alt="tile" />);
    fireEvent.load(screen.getByAltText("tile"));
    rerender(<TileImg src="/t/b.png" alt="tile" />);
    expect(screen.getByAltText("tile")).not.toHaveClass("checkerboard");
    fireEvent.load(screen.getByAltText("tile"));
    expect(screen.getByAltText("tile")).toHaveClass("checkerboard");
  });
});
