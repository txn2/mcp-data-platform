import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

vi.mock("@/api/portal/datahub", () => ({
  useDataHubConnections: vi.fn(),
}));

import { DataHubConnectionSelect, useConnectionWritable } from "./DataHubConnectionSelect";
import { useDataHubConnections } from "@/api/portal/datahub";

type Conn = { name: string; writable: boolean };
const list = (data: Conn[] | undefined, isLoading = false) =>
  ({ data, isLoading }) as never;

beforeEach(() => {
  vi.clearAllMocks();
});

describe("DataHubConnectionSelect", () => {
  it("selects the first connection when none is chosen yet", () => {
    vi.mocked(useDataHubConnections).mockReturnValue(
      list([{ name: "primary", writable: true }, { name: "warehouse", writable: false }]),
    );
    const onChange = vi.fn();
    render(<DataHubConnectionSelect value="" onChange={onChange} />);
    expect(onChange).toHaveBeenCalledWith("primary");
  });

  it("re-selects when the chosen connection is no longer in the list", () => {
    // A persisted selection outlives the connection it names: renamed, removed,
    // or revoked from the persona. With one connection the control is disabled,
    // so leaving the stale value in place would wedge every read on 404s with no
    // way for the reader to recover.
    vi.mocked(useDataHubConnections).mockReturnValue(list([{ name: "primary", writable: true }]));
    const onChange = vi.fn();
    render(<DataHubConnectionSelect value="retired" onChange={onChange} />);
    expect(onChange).toHaveBeenCalledWith("primary");
  });

  it("leaves a valid selection alone", () => {
    vi.mocked(useDataHubConnections).mockReturnValue(
      list([{ name: "primary", writable: true }, { name: "warehouse", writable: true }]),
    );
    const onChange = vi.fn();
    render(<DataHubConnectionSelect value="warehouse" onChange={onChange} />);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("selects nothing while the list is still loading or empty", () => {
    const onChange = vi.fn();
    vi.mocked(useDataHubConnections).mockReturnValue(list(undefined, true));
    const { unmount } = render(<DataHubConnectionSelect value="" onChange={onChange} />);
    unmount();
    vi.mocked(useDataHubConnections).mockReturnValue(list([]));
    render(<DataHubConnectionSelect value="" onChange={onChange} />);
    expect(onChange).not.toHaveBeenCalled();
    // Nothing to pick means nothing to show, rather than an empty control.
    expect(screen.queryByLabelText("DataHub connection")).not.toBeInTheDocument();
  });

  it("flags a read-only connection and reports the choice", () => {
    vi.mocked(useDataHubConnections).mockReturnValue(
      list([{ name: "primary", writable: true }, { name: "archive", writable: false }]),
    );
    const onChange = vi.fn();
    render(<DataHubConnectionSelect value="archive" onChange={onChange} />);
    expect(screen.getByText("read-only")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("DataHub connection"), {
      target: { value: "primary" },
    });
    expect(onChange).toHaveBeenCalledWith("primary");
  });

  it("still renders, disabled, when there is only one connection", () => {
    vi.mocked(useDataHubConnections).mockReturnValue(list([{ name: "primary", writable: true }]));
    render(<DataHubConnectionSelect value="primary" onChange={vi.fn()} />);
    expect(screen.getByLabelText("DataHub connection")).toBeDisabled();
  });
});

describe("useConnectionWritable", () => {
  function Probe({ name }: { name: string }) {
    return <span>{String(useConnectionWritable(name))}</span>;
  }

  it("reports the named connection's writable flag", () => {
    vi.mocked(useDataHubConnections).mockReturnValue(
      list([{ name: "primary", writable: true }, { name: "archive", writable: false }]),
    );
    const { unmount } = render(<Probe name="primary" />);
    expect(screen.getByText("true")).toBeInTheDocument();
    unmount();

    render(<Probe name="archive" />);
    expect(screen.getByText("false")).toBeInTheDocument();
  });

  it("reports not writable for a connection it cannot see", () => {
    vi.mocked(useDataHubConnections).mockReturnValue(list([{ name: "primary", writable: true }]));
    render(<Probe name="gone" />);
    expect(screen.getByText("false")).toBeInTheDocument();
  });
});
