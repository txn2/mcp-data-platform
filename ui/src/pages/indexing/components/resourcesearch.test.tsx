import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ResourceSearchPanel } from "./resourcesearch";

const counts = { full: 412, metadata: 1873, none: 96, explicit: 38, content_bytes: 1024, folders: 14 };

describe("ResourceSearchPanel", () => {
  it("shows the files under each Search setting by the labels the upload form uses", () => {
    render(<ResourceSearchPanel data={{ counts }} isError={false} onReclassify={() => {}} reclassifying={false} />);
    expect(screen.getByText("Contents")).toBeInTheDocument();
    expect(screen.getByText("412")).toBeInTheDocument();
    expect(screen.getByText("Details only")).toBeInTheDocument();
    expect(screen.getByText("1,873")).toBeInTheDocument();
    expect(screen.getByText("Not indexed")).toBeInTheDocument();
    expect(screen.getByText("96")).toBeInTheDocument();
    expect(screen.getByText("Chosen by writers")).toBeInTheDocument();
    expect(screen.getByText(/Applies resources.index again/)).toBeInTheDocument();
  });

  it("starts a reclassification", () => {
    const onReclassify = vi.fn();
    render(<ResourceSearchPanel data={{ counts }} isError={false} onReclassify={onReclassify} reclassifying={false} />);
    fireEvent.click(screen.getByRole("button", { name: "Reclassify" }));
    expect(onReclassify).toHaveBeenCalledOnce();
  });

  it("disables Reclassify while one runs and says so", () => {
    const started = new Date().toISOString();
    render(
      <ResourceSearchPanel
        data={{ counts, last_run: { started_at: started } }}
        isError={false}
        onReclassify={() => {}}
        reclassifying={false}
      />,
    );
    expect(screen.getByRole("button", { name: "Reclassify" })).toBeDisabled();
    expect(screen.getByText(/^Reclassifying, started/)).toBeInTheDocument();
  });

  it("reports the last run's result and a failed run's error", () => {
    const at = new Date().toISOString();
    const { rerender } = render(
      <ResourceSearchPanel
        data={{ counts, last_run: { started_at: at, finished_at: at, result: { examined: 2381, changed: 12, cleared: 3 } } }}
        isError={false}
        onReclassify={() => {}}
        reclassifying={false}
      />,
    );
    expect(screen.getByText(/2,381 files read, 12 changed, 3 had their kept text removed/)).toBeInTheDocument();
    rerender(
      <ResourceSearchPanel
        data={{ counts, last_run: { started_at: at, finished_at: at, error: "database unavailable" } }}
        isError={false}
        onReclassify={() => {}}
        reclassifying={false}
      />,
    );
    expect(screen.getByText("The last reclassification failed: database unavailable")).toBeInTheDocument();
  });

  it("says when the counts cannot be read, and while they load", () => {
    const { rerender } = render(<ResourceSearchPanel isError onReclassify={() => {}} reclassifying={false} />);
    expect(screen.getByText("Could not read how resources are indexed.")).toBeInTheDocument();
    rerender(<ResourceSearchPanel isError={false} onReclassify={() => {}} reclassifying={false} />);
    expect(screen.getByText("Counting resources…")).toBeInTheDocument();
  });
});
