import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";
import { AdminSettingsPage } from "./AdminSettingsPage";
import type { SMTPSettings } from "@/api/admin/hooks/settings";

// Mock the admin hooks the page consumes. ConfigField/ConfigToggle are real
// components, so the assertions below exercise the actual rendered form.
vi.mock("@/api/admin/hooks", () => ({
  useSystemInfo: vi.fn(),
  useSMTPSettings: vi.fn(),
  useSetSMTPSettings: vi.fn(),
  useSendTestEmail: vi.fn(),
  useSMTPRecipientStatus: vi.fn(),
}));

import {
  useSystemInfo,
  useSMTPSettings,
  useSetSMTPSettings,
  useSendTestEmail,
  useSMTPRecipientStatus,
} from "@/api/admin/hooks";

const mockUseSystemInfo = vi.mocked(useSystemInfo);
const mockUseSMTPSettings = vi.mocked(useSMTPSettings);
const mockUseSetSMTPSettings = vi.mocked(useSetSMTPSettings);
const mockUseSendTestEmail = vi.mocked(useSendTestEmail);
const mockUseSMTPRecipientStatus = vi.mocked(useSMTPRecipientStatus);

function makeSettings(overrides: Partial<SMTPSettings> = {}): SMTPSettings {
  return {
    enabled: true,
    host: "smtp.example.com",
    port: 587,
    username: "mailer@example.com",
    password_set: false,
    from: "platform@example.com",
    from_name: "Data Platform",
    tls_mode: "starttls",
    updated_by: "admin@example.com",
    updated_at: "2026-04-10T15:30:00Z",
    ...overrides,
  };
}

const saveMutate = vi.fn();
const testMutate = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  mockUseSystemInfo.mockReturnValue({
    data: { config_mode: "database" },
  } as unknown as ReturnType<typeof useSystemInfo>);
  mockUseSMTPSettings.mockReturnValue({
    data: makeSettings(),
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useSMTPSettings>);
  mockUseSetSMTPSettings.mockReturnValue({
    mutate: saveMutate,
    isPending: false,
  } as unknown as ReturnType<typeof useSetSMTPSettings>);
  mockUseSendTestEmail.mockReturnValue({
    mutate: testMutate,
    isPending: false,
  } as unknown as ReturnType<typeof useSendTestEmail>);
  mockUseSMTPRecipientStatus.mockReturnValue({
    data: undefined,
  } as unknown as ReturnType<typeof useSMTPRecipientStatus>);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("AdminSettingsPage: loading and loaded states", () => {
  it("shows a loading indicator while settings load", () => {
    mockUseSMTPSettings.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useSMTPSettings>);

    render(<AdminSettingsPage />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
    expect(screen.queryByDisplayValue("smtp.example.com")).not.toBeInTheDocument();
  });

  it("renders the loaded form with stored values", () => {
    render(<AdminSettingsPage />);

    expect(screen.getByText("Email (SMTP)")).toBeInTheDocument();
    expect(screen.getByDisplayValue("smtp.example.com")).toBeInTheDocument();
    expect(screen.getByDisplayValue("587")).toBeInTheDocument();
    expect(screen.getByDisplayValue("mailer@example.com")).toBeInTheDocument();
    expect(screen.getByDisplayValue("platform@example.com")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Data Platform")).toBeInTheDocument();
    expect(screen.getByRole("switch")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("combobox")).toHaveValue("starttls");
    expect(screen.getByText(/Updated by admin@example.com/)).toBeInTheDocument();
  });

  it("shows a load-error banner with retry when the fetch fails", () => {
    const refetch = vi.fn();
    mockUseSMTPSettings.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("boom"),
      refetch,
    } as unknown as ReturnType<typeof useSMTPSettings>);

    render(<AdminSettingsPage />);
    expect(screen.getByText(/Failed to load SMTP settings/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(refetch).toHaveBeenCalled();
  });
});

describe("AdminSettingsPage: saving", () => {
  it("saves the edited form and shows the transient Saved state", () => {
    saveMutate.mockImplementation((_input, opts) => {
      opts?.onSuccess?.(makeSettings({ host: "mail.internal", password_set: true }));
    });

    render(<AdminSettingsPage />);

    const saveButton = screen.getByRole("button", { name: /Save/ });
    expect(saveButton).toBeDisabled();

    fireEvent.change(screen.getByDisplayValue("smtp.example.com"), {
      target: { value: "mail.internal" },
    });
    expect(screen.getByText("You have unsaved changes")).toBeInTheDocument();
    fireEvent.click(saveButton);

    expect(saveMutate).toHaveBeenCalledWith(
      {
        enabled: true,
        host: "mail.internal",
        port: 587,
        username: "mailer@example.com",
        password: "",
        from: "platform@example.com",
        from_name: "Data Platform",
        tls_mode: "starttls",
      },
      expect.anything(),
    );
    expect(screen.getByText("Saved")).toBeInTheDocument();
  });

  it("shows the error inline when the save fails", () => {
    saveMutate.mockImplementation((_input, opts) => {
      opts?.onError?.(new Error("invalid port"));
    });

    render(<AdminSettingsPage />);
    fireEvent.change(screen.getByDisplayValue("smtp.example.com"), {
      target: { value: "mail.internal" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));

    expect(screen.getByText("invalid port")).toBeInTheDocument();
    expect(screen.queryByText("Saved")).not.toBeInTheDocument();
  });
});

describe("AdminSettingsPage: read-only file mode", () => {
  it("shows the read-only banner and hides the save and test actions", () => {
    mockUseSystemInfo.mockReturnValue({
      data: { config_mode: "file" },
    } as unknown as ReturnType<typeof useSystemInfo>);

    render(<AdminSettingsPage />);

    expect(screen.getByText(/Configuration is read-only/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Save/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Send test/ })).not.toBeInTheDocument();
  });
});

describe("AdminSettingsPage: write-only password", () => {
  it("shows the (unchanged) placeholder when a password is stored and the field is empty", () => {
    mockUseSMTPSettings.mockReturnValue({
      data: makeSettings({ password_set: true }),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useSMTPSettings>);

    render(<AdminSettingsPage />);
    const password = screen.getByPlaceholderText("(unchanged)");
    expect(password).toHaveAttribute("type", "password");
    expect(password).toHaveValue("");
  });

  it("has no placeholder when no password is stored", () => {
    render(<AdminSettingsPage />);
    expect(screen.queryByPlaceholderText("(unchanged)")).not.toBeInTheDocument();
  });
});

describe("AdminSettingsPage: send test email", () => {
  it("sends to the entered recipient and shows the confirmation", () => {
    testMutate.mockImplementation((to, opts) => {
      opts?.onSuccess?.({ status: "sent", to });
    });

    render(<AdminSettingsPage />);

    const sendButton = screen.getByRole("button", { name: /Send test/ });
    expect(sendButton).toBeDisabled();

    fireEvent.change(screen.getByPlaceholderText("recipient@example.com"), {
      target: { value: "user@example.com" },
    });
    fireEvent.click(sendButton);

    expect(testMutate).toHaveBeenCalledWith("user@example.com", expect.anything());
    expect(screen.getByText("Test email sent to user@example.com")).toBeInTheDocument();
  });

  it("shows the API error detail inline when the send fails", () => {
    testMutate.mockImplementation((_to, opts) => {
      opts?.onError?.(new Error("SMTP connection refused"));
    });

    render(<AdminSettingsPage />);
    fireEvent.change(screen.getByPlaceholderText("recipient@example.com"), {
      target: { value: "user@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Send test/ }));

    expect(screen.getByText("SMTP connection refused")).toBeInTheDocument();
  });

  it("disables the button while a send is pending", () => {
    mockUseSendTestEmail.mockReturnValue({
      mutate: testMutate,
      isPending: true,
    } as unknown as ReturnType<typeof useSendTestEmail>);

    render(<AdminSettingsPage />);
    fireEvent.change(screen.getByPlaceholderText("recipient@example.com"), {
      target: { value: "user@example.com" },
    });
    expect(screen.getByRole("button", { name: /Sending/ })).toBeDisabled();
  });
});

describe("AdminSettingsPage: test-send opt-out notice (#1022)", () => {
  const OPT_OUT_NOTICE = /opted out of notification emails; the test will still send/;

  it("shows the informational notice for an opted-out target", () => {
    vi.useFakeTimers();
    mockUseSMTPRecipientStatus.mockReturnValue({
      data: { to: "optedout@example.com", opted_out: true },
    } as unknown as ReturnType<typeof useSMTPRecipientStatus>);

    render(<AdminSettingsPage />);
    // Mixed case in the input must still match the canonical status address.
    fireEvent.change(screen.getByPlaceholderText("recipient@example.com"), {
      target: { value: "OptedOut@example.com" },
    });
    act(() => {
      vi.advanceTimersByTime(500);
    });

    expect(screen.getByText(OPT_OUT_NOTICE)).toBeInTheDocument();
    // Informational only: the send action stays enabled.
    expect(screen.getByRole("button", { name: /Send test/ })).toBeEnabled();
  });

  it("shows no notice when the target has not opted out", () => {
    vi.useFakeTimers();
    mockUseSMTPRecipientStatus.mockReturnValue({
      data: { to: "user@example.com", opted_out: false },
    } as unknown as ReturnType<typeof useSMTPRecipientStatus>);

    render(<AdminSettingsPage />);
    fireEvent.change(screen.getByPlaceholderText("recipient@example.com"), {
      target: { value: "user@example.com" },
    });
    act(() => {
      vi.advanceTimersByTime(500);
    });

    expect(screen.queryByText(OPT_OUT_NOTICE)).not.toBeInTheDocument();
  });

  it("shows no stale notice for a different address than the status answers", () => {
    vi.useFakeTimers();
    mockUseSMTPRecipientStatus.mockReturnValue({
      data: { to: "optedout@example.com", opted_out: true },
    } as unknown as ReturnType<typeof useSMTPRecipientStatus>);

    render(<AdminSettingsPage />);
    fireEvent.change(screen.getByPlaceholderText("recipient@example.com"), {
      target: { value: "other@example.com" },
    });
    act(() => {
      vi.advanceTimersByTime(500);
    });

    expect(screen.queryByText(OPT_OUT_NOTICE)).not.toBeInTheDocument();
  });
});
