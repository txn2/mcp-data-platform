import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { ConnectionAlertCard } from "./ConnectionAlertCard";
import type { ConnectionAlertSettings } from "@/api/admin/hooks/settings";

// Mock the admin hooks the card consumes. The form controls are real, so the
// assertions exercise the rendered card.
vi.mock("@/api/admin/hooks", () => ({
  useConnectionAlert: vi.fn(),
  useSetConnectionAlert: vi.fn(),
}));

import { useConnectionAlert, useSetConnectionAlert } from "@/api/admin/hooks";

const mockUseAlert = vi.mocked(useConnectionAlert);
const mockUseSetAlert = vi.mocked(useSetConnectionAlert);

function makeAlert(overrides: Partial<ConnectionAlertSettings> = {}): ConnectionAlertSettings {
  return {
    enabled: true,
    escalate_after_hours: 24,
    recipients: ["platform-admin@example.com"],
    updated_by: "admin@example.com",
    updated_at: "2026-09-11T16:40:00Z",
    ...overrides,
  };
}

const saveMutate = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  mockUseAlert.mockReturnValue({
    data: makeAlert(),
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useConnectionAlert>);
  mockUseSetAlert.mockReturnValue({
    mutate: saveMutate,
    isPending: false,
  } as unknown as ReturnType<typeof useSetConnectionAlert>);
});

afterEach(cleanup);

describe("ConnectionAlertCard: loaded state", () => {
  it("renders the stored window, escalation recipients, and last writer", () => {
    render(<ConnectionAlertCard isReadOnly={false} />);

    expect(screen.getByText("Connection revocation alerts")).toBeInTheDocument();
    expect(screen.getByDisplayValue("24")).toBeInTheDocument();
    expect(screen.getByText("platform-admin@example.com")).toBeInTheDocument();
    expect(screen.getByRole("switch")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByText(/Updated by admin@example.com/)).toBeInTheDocument();
  });

  it("shows a loading indicator while settings load", () => {
    mockUseAlert.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useConnectionAlert>);

    render(<ConnectionAlertCard isReadOnly={false} />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("shows a load-error banner with retry when the fetch fails", () => {
    const refetch = vi.fn();
    mockUseAlert.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("boom"),
      refetch,
    } as unknown as ReturnType<typeof useConnectionAlert>);

    render(<ConnectionAlertCard isReadOnly={false} />);
    expect(screen.getByText(/Failed to load these alert settings/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(refetch).toHaveBeenCalled();
  });
});

describe("ConnectionAlertCard: escalating nowhere", () => {
  it("shows the server's warning that only the authorizing operator is told", () => {
    mockUseAlert.mockReturnValue({
      data: makeAlert({
        recipients: [],
        warnings: [
          "no escalation recipients are configured, so only the person who authorized a connection is told when it is revoked",
        ],
      }),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useConnectionAlert>);

    render(<ConnectionAlertCard isReadOnly={false} />);
    expect(screen.getByText(/only the person who authorized a connection is told/)).toBeInTheDocument();
  });
});

describe("ConnectionAlertCard: editing", () => {
  it("saves the window and the recipients the operator entered", () => {
    render(<ConnectionAlertCard isReadOnly={false} />);

    fireEvent.change(screen.getByDisplayValue("24"), { target: { value: "6" } });
    fireEvent.change(screen.getByLabelText("Add recipient"), {
      target: { value: "oncall@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Add/ }));
    fireEvent.click(screen.getByRole("button", { name: /^Save/ }));

    expect(saveMutate).toHaveBeenCalledWith(
      {
        enabled: true,
        escalate_after_hours: 6,
        recipients: ["platform-admin@example.com", "oncall@example.com"],
      },
      expect.anything(),
    );
  });

  it("removes a recipient the operator no longer wants told", () => {
    render(<ConnectionAlertCard isReadOnly={false} />);

    fireEvent.click(screen.getByRole("button", { name: "Remove platform-admin@example.com" }));
    fireEvent.click(screen.getByRole("button", { name: /^Save/ }));

    expect(saveMutate).toHaveBeenCalledWith(
      expect.objectContaining({ recipients: [] }),
      expect.anything(),
    );
  });

  it("warns about unsaved changes and reports a failed save", () => {
    saveMutate.mockImplementation((_input, handlers) => {
      handlers.onError(new Error("storing connection alert settings failed"));
    });
    render(<ConnectionAlertCard isReadOnly={false} />);

    fireEvent.click(screen.getByRole("switch"));
    expect(screen.getByText(/unsaved changes/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^Save/ }));
    expect(screen.getByText(/storing connection alert settings failed/)).toBeInTheDocument();
  });
});

describe("ConnectionAlertCard: file config mode", () => {
  it("states the settings are read-only and offers no save", () => {
    render(<ConnectionAlertCard isReadOnly />);

    expect(screen.queryByRole("button", { name: /^Save/ })).not.toBeInTheDocument();
    expect(screen.getByText("platform-admin@example.com")).toBeInTheDocument();
  });
});
