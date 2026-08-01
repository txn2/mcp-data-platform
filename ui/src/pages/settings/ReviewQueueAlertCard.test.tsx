import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import { ReviewQueueAlertCard } from "./ReviewQueueAlertCard";
import { AdminSettingsPage } from "./AdminSettingsPage";
import type { ReviewQueueAlertSettings } from "@/api/admin/hooks/settings";

// Mock the admin hooks both surfaces consume. The form controls are real, so
// the assertions exercise the rendered card.
vi.mock("@/api/admin/hooks", () => ({
  useSystemInfo: vi.fn(),
  useSMTPSettings: vi.fn(),
  useSetSMTPSettings: vi.fn(),
  useSendTestEmail: vi.fn(),
  useSMTPRecipientStatus: vi.fn(),
  useReviewQueueAlert: vi.fn(),
  useSetReviewQueueAlert: vi.fn(),
}));

import {
  useSystemInfo,
  useSMTPSettings,
  useSetSMTPSettings,
  useSendTestEmail,
  useSMTPRecipientStatus,
  useReviewQueueAlert,
  useSetReviewQueueAlert,
} from "@/api/admin/hooks";

const mockUseSystemInfo = vi.mocked(useSystemInfo);
const mockUseAlert = vi.mocked(useReviewQueueAlert);
const mockUseSetAlert = vi.mocked(useSetReviewQueueAlert);

function makeAlert(
  overrides: Partial<ReviewQueueAlertSettings> = {},
): ReviewQueueAlertSettings {
  return {
    enabled: true,
    pending_threshold: 25,
    oldest_pending_days: 30,
    cooldown_hours: 24,
    recipients: ["ops@example.com"],
    updated_by: "admin@example.com",
    updated_at: "2026-07-30T15:30:00Z",
    ...overrides,
  };
}

const saveMutate = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  mockUseSystemInfo.mockReturnValue({
    data: { config_mode: "database" },
  } as unknown as ReturnType<typeof useSystemInfo>);
  mockUseAlert.mockReturnValue({
    data: makeAlert(),
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useReviewQueueAlert>);
  mockUseSetAlert.mockReturnValue({
    mutate: saveMutate,
    isPending: false,
  } as unknown as ReturnType<typeof useSetReviewQueueAlert>);
  // The SMTP section is not under test here; it only has to render.
  vi.mocked(useSMTPSettings).mockReturnValue({
    data: undefined,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useSMTPSettings>);
  vi.mocked(useSetSMTPSettings).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
  } as unknown as ReturnType<typeof useSetSMTPSettings>);
  vi.mocked(useSendTestEmail).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
  } as unknown as ReturnType<typeof useSendTestEmail>);
  vi.mocked(useSMTPRecipientStatus).mockReturnValue({
    data: undefined,
  } as unknown as ReturnType<typeof useSMTPRecipientStatus>);
});

afterEach(cleanup);

describe("ReviewQueueAlertCard: loaded state", () => {
  it("renders the stored thresholds, recipients, and last writer", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);

    expect(screen.getByText("Review queue alert")).toBeInTheDocument();
    expect(screen.getByDisplayValue("25")).toBeInTheDocument();
    expect(screen.getByDisplayValue("30")).toBeInTheDocument();
    expect(screen.getByDisplayValue("24")).toBeInTheDocument();
    expect(screen.getByText("ops@example.com")).toBeInTheDocument();
    expect(screen.getByRole("switch")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByText(/Updated by admin@example.com/)).toBeInTheDocument();
  });

  it("shows a loading indicator while settings load", () => {
    mockUseAlert.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useReviewQueueAlert>);

    render(<ReviewQueueAlertCard isReadOnly={false} />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("shows a load-error banner with retry when the fetch fails", () => {
    const refetch = vi.fn();
    mockUseAlert.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("boom"),
      refetch,
    } as unknown as ReturnType<typeof useReviewQueueAlert>);

    render(<ReviewQueueAlertCard isReadOnly={false} />);
    expect(
      screen.getByText(/Failed to load review-queue alert settings/),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(refetch).toHaveBeenCalled();
  });
});

describe("ReviewQueueAlertCard: undeliverable configuration", () => {
  it("shows the server's warning that nobody will be alerted", () => {
    mockUseAlert.mockReturnValue({
      data: makeAlert({
        recipients: [],
        warnings: ["no recipients are configured, so no alert will be delivered; add at least one address"],
      }),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useReviewQueueAlert>);

    render(<ReviewQueueAlertCard isReadOnly={false} />);
    expect(screen.getByText(/no alert will be delivered/)).toBeInTheDocument();
  });

  it("states that a recipient's own opt-out still applies", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);
    expect(
      screen.getByText(/turned email notifications off receives nothing/),
    ).toBeInTheDocument();
  });
});

describe("ReviewQueueAlertCard: recipients", () => {
  it("adds a typed recipient and includes it in the save", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);

    fireEvent.change(screen.getByLabelText("Add recipient"), {
      target: { value: "lead@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Add/ }));
    expect(screen.getByText("lead@example.com")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    expect(saveMutate).toHaveBeenCalledWith(
      expect.objectContaining({
        recipients: ["ops@example.com", "lead@example.com"],
      }),
      expect.anything(),
    );
  });

  it("adds on Enter without submitting anything else", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);

    const input = screen.getByLabelText("Add recipient");
    fireEvent.change(input, { target: { value: "lead@example.com" } });
    fireEvent.keyDown(input, { key: "Enter" });

    expect(screen.getByText("lead@example.com")).toBeInTheDocument();
    expect(input).toHaveValue("");
    expect(saveMutate).not.toHaveBeenCalled();
  });

  it("commits an address typed but not added when the field loses focus", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);

    const input = screen.getByLabelText("Add recipient");
    fireEvent.change(input, { target: { value: "lead@example.com" } });
    // The operator reaches straight for Save without pressing Add.
    fireEvent.blur(input);
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));

    expect(saveMutate).toHaveBeenCalledWith(
      expect.objectContaining({
        recipients: ["ops@example.com", "lead@example.com"],
      }),
      expect.anything(),
    );
  });

  it("ignores a duplicate address", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);

    fireEvent.change(screen.getByLabelText("Add recipient"), {
      target: { value: "ops@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Add/ }));

    const list = screen.getByRole("list");
    expect(within(list).getAllByText("ops@example.com")).toHaveLength(1);
  });

  it("removes a recipient", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);

    fireEvent.click(screen.getByRole("button", { name: "Remove ops@example.com" }));
    expect(screen.queryByText("ops@example.com")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    expect(saveMutate).toHaveBeenCalledWith(
      expect.objectContaining({ recipients: [] }),
      expect.anything(),
    );
  });
});

describe("ReviewQueueAlertCard: saving", () => {
  it("saves the edited thresholds as numbers", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);

    fireEvent.change(screen.getByDisplayValue("25"), { target: { value: "40" } });
    fireEvent.change(screen.getByDisplayValue("24"), { target: { value: "6" } });
    expect(screen.getByText("You have unsaved changes")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    expect(saveMutate).toHaveBeenCalledWith(
      {
        enabled: true,
        pending_threshold: 40,
        oldest_pending_days: 30,
        cooldown_hours: 6,
        recipients: ["ops@example.com"],
      },
      expect.anything(),
    );
  });

  it("sends 0 for a cleared threshold, keeping the condition off rather than guessing", () => {
    render(<ReviewQueueAlertCard isReadOnly={false} />);

    fireEvent.change(screen.getByDisplayValue("25"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));

    expect(saveMutate).toHaveBeenCalledWith(
      expect.objectContaining({ pending_threshold: 0 }),
      expect.anything(),
    );
  });

  it("shows the error inline when the save fails", () => {
    saveMutate.mockImplementation((_input, opts) => {
      opts.onError(new Error("pending_threshold must be between 0 and 1000000"));
    });

    render(<ReviewQueueAlertCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("switch"));
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));

    expect(
      screen.getByText("pending_threshold must be between 0 and 1000000"),
    ).toBeInTheDocument();
  });
});

describe("ReviewQueueAlertCard: read-only file mode", () => {
  it("shows the read-only banner and hides Save", () => {
    render(<ReviewQueueAlertCard isReadOnly={true} />);

    expect(screen.getByText(/Configuration is read-only/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Save/ })).not.toBeInTheDocument();
  });
});

describe("AdminSettingsPage composition", () => {
  it("renders both settings sections", () => {
    render(<AdminSettingsPage />);
    expect(screen.getByText("Email (SMTP)")).toBeInTheDocument();
    expect(screen.getByText("Review queue alert")).toBeInTheDocument();
  });
});
