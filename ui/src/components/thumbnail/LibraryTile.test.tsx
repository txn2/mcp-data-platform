import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { LIBRARY_TILE_TYPE, LibraryTile } from "./LibraryTile";
import { Tile } from "./Tile";

afterEach(cleanup);

describe("LibraryTile (#1970)", () => {
  it("draws the library's name and version, never an empty diagram, and says it is drawn", async () => {
    const onDrawn = vi.fn();
    render(<LibraryTile name="date-windows" content={JSON.stringify({ version: 2 })} onDrawn={onDrawn} />);
    const tile = screen.getByTestId("library-tile");
    expect(tile).toHaveTextContent("date-windows");
    expect(tile).toHaveTextContent("Version 2");
    expect(tile.querySelector("svg")).not.toBeNull();
    expect(screen.queryByText("No platform calls")).not.toBeInTheDocument();
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(""));
  });

  it("reports content it cannot read rather than drawing a blank tile", () => {
    const onDrawn = vi.fn();
    render(<LibraryTile name="x" content="{}" onDrawn={onDrawn} />);
    expect(onDrawn).toHaveBeenCalledWith("the library tile could not be read");
    expect(screen.queryByTestId("library-tile")).not.toBeInTheDocument();
    render(<LibraryTile name="x" content="not json" onDrawn={onDrawn} />);
    expect(onDrawn).toHaveBeenCalledTimes(2);
  });

  it("is what the tile page draws for the library content type", () => {
    render(
      <Tile
        data={{ contentType: LIBRARY_TILE_TYPE, name: "date-windows", content: '{"version":3}', contentURL: "/content", serveFromURL: false }}
        dark
        onDrawn={vi.fn()}
      />,
    );
    expect(screen.getByTestId("library-tile")).toHaveTextContent("Version 3");
  });
});
