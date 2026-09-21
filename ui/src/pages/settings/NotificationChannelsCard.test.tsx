import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { NotificationChannelsCard } from "./NotificationChannelsCard";
import type { NotificationChannel } from "@/api/admin/hooks/settings";

// Mock the admin hooks the card consumes. The form controls are real, so the
// assertions exercise the rendered card.
vi.mock("@/api/admin/hooks", () => ({
  useNotificationChannels: vi.fn(),
  useSetNotificationChannel: vi.fn(),
  useDeleteNotificationChannel: vi.fn(),
  useTestNotificationChannel: vi.fn(),
}));

import {
  useNotificationChannels,
  useSetNotificationChannel,
  useDeleteNotificationChannel,
  useTestNotificationChannel,
} from "@/api/admin/hooks";

const mockList = vi.mocked(useNotificationChannels);
const mockSave = vi.mocked(useSetNotificationChannel);
const mockDelete = vi.mocked(useDeleteNotificationChannel);
const mockTest = vi.mocked(useTestNotificationChannel);

function makeChannel(overrides: Partial<NotificationChannel> = {}): NotificationChannel {
  return {
    name: "ops-alerts",
    kind: "mattermost",
    description: "Operations alerts",
    enabled: true,
    connection: "chat-bot",
    target: "C0123456789",
    mode: "immediate",
    repeat_after: "1h0m0s",
    max_per_hour: 60,
    created_by: "admin@example.com",
    ...overrides,
  };
}

const saveMutate = vi.fn();
const deleteMutate = vi.fn();
const testMutate = vi.fn();

function setChannels(channels: NotificationChannel[]) {
  mockList.mockReturnValue({
    data: { channels, kinds: ["mattermost", "webhook", "email"] },
    isLoading: false,
    error: null,
  } as unknown as ReturnType<typeof useNotificationChannels>);
}

beforeEach(() => {
  vi.clearAllMocks();
  setChannels([makeChannel()]);
  mockSave.mockReturnValue({ mutate: saveMutate, isPending: false } as unknown as ReturnType<
    typeof useSetNotificationChannel
  >);
  mockDelete.mockReturnValue({ mutate: deleteMutate, isPending: false } as unknown as ReturnType<
    typeof useDeleteNotificationChannel
  >);
  mockTest.mockReturnValue({ mutate: testMutate, isPending: false } as unknown as ReturnType<
    typeof useTestNotificationChannel
  >);
});

afterEach(cleanup);

describe("NotificationChannelsCard", () => {
  it("lists each channel with its kind", () => {
    setChannels([makeChannel(), makeChannel({ name: "ops-email", kind: "email", connection: undefined, recipients: ["ops@example.com"] })]);
    render(<NotificationChannelsCard isReadOnly={false} />);

    expect(screen.getByText("ops-alerts")).toBeInTheDocument();
    expect(screen.getByText("Mattermost")).toBeInTheDocument();
    expect(screen.getByText("ops-email")).toBeInTheDocument();
    expect(screen.getByText("Email list")).toBeInTheDocument();
  });

  it("says plainly when nothing is configured", () => {
    setChannels([]);
    render(<NotificationChannelsCard isReadOnly={false} />);
    expect(screen.getByText(/No channels are configured/)).toBeInTheDocument();
  });

  it("opens the editor on row click, as every portal list does", () => {
    render(<NotificationChannelsCard isReadOnly={false} />);
    expect(screen.queryByTestId("channel-editor")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("channel-row-ops-alerts"));

    expect(screen.getByTestId("channel-editor")).toBeInTheDocument();
    expect(screen.getByDisplayValue("C0123456789")).toBeInTheDocument();
  });

  it("shows the fields the chosen kind needs and hides the ones it cannot use", () => {
    render(<NotificationChannelsCard isReadOnly={false} />);
    fireEvent.click(screen.getByTestId("channel-new"));

    // mattermost is the default: a connection and a target.
    expect(screen.getByText("Target channel id")).toBeInTheDocument();
    expect(screen.getByText("Connection")).toBeInTheDocument();
    expect(screen.queryByText("Recipients")).not.toBeInTheDocument();

    // A webhook posts where its URL points, so it has no target.
    fireEvent.click(screen.getByTestId("channel-kind-webhook"));
    expect(screen.queryByText("Target channel id")).not.toBeInTheDocument();
    expect(screen.getByText("Connection")).toBeInTheDocument();

    // An email list names no connection.
    fireEvent.click(screen.getByTestId("channel-kind-email"));
    expect(screen.queryByText("Connection")).not.toBeInTheDocument();
    expect(screen.getByText("Recipients")).toBeInTheDocument();
  });

  it("saves only the fields the chosen kind can use", () => {
    // A kind switched in the form must not save a leftover from the one
    // before it: the API would refuse it, naming a field that is no longer
    // on screen.
    render(<NotificationChannelsCard isReadOnly={false} />);
    fireEvent.click(screen.getByTestId("channel-row-ops-alerts"));
    fireEvent.click(screen.getByTestId("channel-kind-email"));
    fireEvent.click(screen.getByRole("button", { name: /Save channel/ }));

    expect(saveMutate).toHaveBeenCalledTimes(1);
    const { name, input } = saveMutate.mock.calls[0]![0];
    expect(name).toBe("ops-alerts");
    expect(input.kind).toBe("email");
    expect(input.target).toBe("");
    expect(input.connection).toBe("");
  });

  it("reports the upstream's own answer to a test send", () => {
    testMutate.mockImplementation((_name: string, opts: { onSuccess: (r: unknown) => void }) => {
      opts.onSuccess({ delivered: true, detail: "the channel's upstream accepted the test message" });
    });
    render(<NotificationChannelsCard isReadOnly={false} />);
    fireEvent.click(screen.getByTestId("channel-row-ops-alerts"));
    fireEvent.click(screen.getByTestId("channel-test"));

    expect(screen.getByTestId("channel-test-result")).toHaveTextContent("accepted the test message");
  });

  it("shows a refused test in the transport's words", () => {
    // The upstream's own words are the only thing that tells the
    // administrator what to fix, so they reach the screen rather than a
    // generic failure.
    testMutate.mockImplementation((_name: string, opts: { onError: (e: Error) => void }) => {
      opts.onError(new Error("mattermost refused the message: the bot is not in that channel"));
    });
    render(<NotificationChannelsCard isReadOnly={false} />);
    fireEvent.click(screen.getByTestId("channel-row-ops-alerts"));
    fireEvent.click(screen.getByTestId("channel-test"));

    expect(screen.getByTestId("channel-test-result")).toHaveTextContent("not in that channel");
  });

  it("offers no test for a channel that does not exist yet", () => {
    // A test delivers through what is stored, not through what is on screen.
    render(<NotificationChannelsCard isReadOnly={false} />);
    fireEvent.click(screen.getByTestId("channel-new"));
    expect(screen.queryByTestId("channel-test")).not.toBeInTheDocument();
    expect(screen.queryByTestId("channel-delete")).not.toBeInTheDocument();
  });

  it("surfaces a channel that cannot deliver", () => {
    setChannels([
      makeChannel({
        connection: "ghost",
        warnings: ['no api connection named "ghost" is served here, so nothing sent to this channel can be delivered'],
      }),
    ]);
    render(<NotificationChannelsCard isReadOnly={false} />);
    expect(screen.getByText(/no api connection named "ghost"/)).toBeInTheDocument();
  });

  it("marks a disabled channel and a daily one", () => {
    setChannels([makeChannel({ enabled: false, mode: "daily" })]);
    render(<NotificationChannelsCard isReadOnly={false} />);
    expect(screen.getByText("disabled")).toBeInTheDocument();
    expect(screen.getByText("daily digest")).toBeInTheDocument();
  });

  it("offers no editing in file config mode", () => {
    render(<NotificationChannelsCard isReadOnly={true} />);
    expect(screen.queryByTestId("channel-new")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("channel-row-ops-alerts"));
    expect(screen.getByRole("button", { name: /Save channel/ })).toBeDisabled();
    expect(screen.getByTestId("channel-test")).toBeDisabled();
  });

  it("deletes the open channel", () => {
    render(<NotificationChannelsCard isReadOnly={false} />);
    fireEvent.click(screen.getByTestId("channel-row-ops-alerts"));
    fireEvent.click(screen.getByTestId("channel-delete"));
    expect(deleteMutate).toHaveBeenCalledWith("ops-alerts", expect.anything());
  });

  it("shows a save failure in the words the API used", () => {
    saveMutate.mockImplementation((_args: unknown, opts: { onError: (e: Error) => void }) => {
      opts.onError(new Error("a mattermost channel needs a target channel id to post to"));
    });
    render(<NotificationChannelsCard isReadOnly={false} />);
    fireEvent.click(screen.getByTestId("channel-row-ops-alerts"));
    fireEvent.click(screen.getByRole("button", { name: /Save channel/ }));

    expect(screen.getByTestId("channel-save-error")).toHaveTextContent("target channel id");
  });
});
