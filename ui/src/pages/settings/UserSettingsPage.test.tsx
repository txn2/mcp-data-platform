import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { UserSettingsPage } from "./UserSettingsPage";
import type { NotificationPrefs } from "@/api/portal/hooks/notifications";

// Mock the portal hooks the page consumes. ConfigToggle is real, so the
// switch assertions below exercise the actual rendered controls.
vi.mock("@/api/portal/hooks", () => ({
  useNotificationPrefs: vi.fn(),
  useSetNotificationPrefs: vi.fn(),
}));

// The no-delivery notice offers admins the way in to SMTP settings, so the
// page reads the auth store. Drive is_admin per test.
vi.mock("@/stores/auth", () => ({
  useAuthStore: (selector: (s: { isAdmin: () => boolean }) => unknown) =>
    selector({ isAdmin: () => admin }),
}));

let admin = false;

import { useNotificationPrefs, useSetNotificationPrefs } from "@/api/portal/hooks";

const mockUsePrefs = vi.mocked(useNotificationPrefs);
const mockUseSetPrefs = vi.mocked(useSetNotificationPrefs);

function makePrefs(overrides: Partial<NotificationPrefs> = {}): NotificationPrefs {
  return {
    mode: "immediate",
    shares_enabled: true,
    comments_enabled: true,
    mentions_enabled: true,
    delivery_available: true,
    ...overrides,
  };
}

const saveMutate = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  admin = false;
  mockUsePrefs.mockReturnValue({
    data: makePrefs(),
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  } as unknown as ReturnType<typeof useNotificationPrefs>);
  mockUseSetPrefs.mockReturnValue({
    mutate: saveMutate,
    isPending: false,
  } as unknown as ReturnType<typeof useSetNotificationPrefs>);
});

afterEach(cleanup);

describe("UserSettingsPage: loading and loaded states", () => {
  it("shows a loading indicator while preferences load", () => {
    mockUsePrefs.mockReturnValue({
      data: undefined,
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useNotificationPrefs>);

    render(<UserSettingsPage />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
    expect(screen.queryByRole("radiogroup")).not.toBeInTheDocument();
  });

  it("renders the delivery modes and category toggles", () => {
    render(<UserSettingsPage />);

    expect(screen.getByText("Notifications")).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: "Off" })).toHaveAttribute("aria-checked", "false");
    expect(screen.getByRole("radio", { name: "Immediate" })).toHaveAttribute("aria-checked", "true");
    expect(screen.getByRole("radio", { name: "Daily digest" })).toHaveAttribute("aria-checked", "false");
    expect(screen.getByText("One email per event")).toBeInTheDocument();

    const switches = screen.getAllByRole("switch");
    expect(switches).toHaveLength(3);
    for (const toggle of switches) {
      expect(toggle).toHaveAttribute("aria-checked", "true");
    }
    expect(screen.getByText("Shares")).toBeInTheDocument();
    expect(screen.getByText("Comments and feedback")).toBeInTheDocument();
  });

  it("dims and disables the category toggles when delivery is off", () => {
    mockUsePrefs.mockReturnValue({
      data: makePrefs({ mode: "off" }),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useNotificationPrefs>);

    render(<UserSettingsPage />);
    const categories = screen.getByTestId("notification-categories");
    expect(categories.className).toContain("pointer-events-none");
    expect(categories.className).toContain("opacity-50");
    expect(categories).toHaveAttribute("aria-disabled", "true");
  });

  it("shows a load-error banner with retry when the fetch fails", () => {
    const refetch = vi.fn();
    mockUsePrefs.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error("boom"),
      refetch,
    } as unknown as ReturnType<typeof useNotificationPrefs>);

    render(<UserSettingsPage />);
    expect(screen.getByText(/Failed to load notification preferences/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(refetch).toHaveBeenCalled();
  });
});

describe("UserSettingsPage: saving on change", () => {
  it("saves the mode as a partial update and shows the transient Saved state", () => {
    saveMutate.mockImplementation((_input, opts) => {
      opts?.onSuccess?.(makePrefs({ mode: "daily" }));
    });

    render(<UserSettingsPage />);
    fireEvent.click(screen.getByRole("radio", { name: "Daily digest" }));

    expect(saveMutate).toHaveBeenCalledWith({ mode: "daily" }, expect.anything());
    expect(screen.getByText("Saved")).toBeInTheDocument();
  });

  it("saves a category toggle as a partial update", () => {
    saveMutate.mockImplementation((_input, opts) => {
      opts?.onSuccess?.(makePrefs({ shares_enabled: false }));
    });

    render(<UserSettingsPage />);
    fireEvent.click(screen.getAllByRole("switch")[0]!);

    expect(saveMutate).toHaveBeenCalledWith({ shares_enabled: false }, expect.anything());
    expect(screen.getByText("Saved")).toBeInTheDocument();
  });

  it("shows the error inline when the save fails", () => {
    saveMutate.mockImplementation((_input, opts) => {
      opts?.onError?.(new Error("store unavailable"));
    });

    render(<UserSettingsPage />);
    fireEvent.click(screen.getByRole("radio", { name: "Off" }));

    expect(screen.getByText("store unavailable")).toBeInTheDocument();
    expect(screen.queryByText("Saved")).not.toBeInTheDocument();
  });
});

// With no SMTP delivery path the section stays visible with the user's stored
// values, but nothing in it is live: the settings would describe an intent the
// platform cannot act on (#1099).
describe("UserSettingsPage: no delivery path configured", () => {
  function renderUnavailable(onNavigate?: (path: string) => void) {
    mockUsePrefs.mockReturnValue({
      data: makePrefs({ delivery_available: false }),
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    } as unknown as ReturnType<typeof useNotificationPrefs>);
    render(<UserSettingsPage onNavigate={onNavigate} />);
  }

  it("disables the mode radios and every category toggle, and explains why", () => {
    renderUnavailable();

    for (const label of ["Off", "Immediate", "Daily digest"]) {
      expect(screen.getByRole("radio", { name: label })).toBeDisabled();
    }
    const switches = screen.getAllByRole("switch");
    expect(switches).toHaveLength(3);
    for (const toggle of switches) {
      expect(toggle).toBeDisabled();
    }
    expect(screen.getByTestId("no-delivery-notice")).toBeInTheDocument();
    expect(screen.getByText(/Email delivery is not configured/)).toBeInTheDocument();
  });

  it("leaves the controls live and the note absent when delivery is available", () => {
    render(<UserSettingsPage />);

    for (const label of ["Off", "Immediate", "Daily digest"]) {
      expect(screen.getByRole("radio", { name: label })).toBeEnabled();
    }
    for (const toggle of screen.getAllByRole("switch")) {
      expect(toggle).toBeEnabled();
    }
    expect(screen.queryByTestId("no-delivery-notice")).not.toBeInTheDocument();
  });

  it("stores nothing while the controls are inert", () => {
    renderUnavailable();

    fireEvent.click(screen.getByRole("radio", { name: "Daily digest" }));
    fireEvent.click(screen.getAllByRole("switch")[0]!);
    expect(saveMutate).not.toHaveBeenCalled();
  });

  it("gives an admin the way in to the SMTP settings page", () => {
    admin = true;
    const onNavigate = vi.fn();
    renderUnavailable(onNavigate);

    const link = screen.getByRole("button", { name: /Configure SMTP/ });
    fireEvent.click(link);
    expect(onNavigate).toHaveBeenCalledWith("/admin/settings");
  });

  it("offers a non-admin no SMTP link, since they have no way to set it", () => {
    renderUnavailable();

    expect(screen.getByTestId("no-delivery-notice")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Configure SMTP/ })).not.toBeInTheDocument();
  });
});
