import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";

vi.mock("@/api/portal/hooks", () => ({
  usePortalChannels: vi.fn(),
  usePublishToChannel: vi.fn(),
}));

import { usePortalChannels, usePublishToChannel } from "@/api/portal/hooks";
import { PostToChannelDialog } from "./PostToChannelDialog";

const mutate = vi.fn();
const reset = vi.fn();

function channels(data: unknown, state: { isLoading?: boolean; error?: unknown } = {}) {
  vi.mocked(usePortalChannels).mockReturnValue({
    data,
    isLoading: state.isLoading ?? false,
    error: state.error ?? null,
  } as unknown as ReturnType<typeof usePortalChannels>);
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(usePublishToChannel).mockReturnValue({ mutate, reset, isPending: false } as unknown as ReturnType<
    typeof usePublishToChannel
  >);
  channels({
    channels: [{ name: "ops-alerts", kind: "mattermost", mode: "immediate", carries: "a title, a markdown body, files and a link" }],
  });
});

afterEach(cleanup);

function chooseChannel(name: string) {
  fireEvent.keyDown(screen.getByRole("combobox", { name: "Channel" }), { key: "Enter" });
  fireEvent.click(screen.getByRole("option", { name }));
}

describe("PostToChannelDialog", () => {
  it("posts the asset with the message to the chosen channel and reports the answer", () => {
    mutate.mockImplementation((_input, opts: { onSuccess: (r: unknown) => void }) =>
      opts.onSuccess({ channel: "ops-alerts", queued: 1, detail: 'accepted for delivery to "ops-alerts"' }),
    );
    render(<PostToChannelDialog assetId="a1" open onOpenChange={vi.fn()} />);
    expect(screen.getByRole("button", { name: /post/i })).toBeDisabled();

    chooseChannel("ops-alerts");
    expect(screen.getByText(/Shows a title, a markdown body/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Message"), { target: { value: "This week" } });
    fireEvent.click(screen.getByRole("button", { name: /^post$/i }));

    expect(mutate).toHaveBeenCalledWith({ channel: "ops-alerts", assetId: "a1", message: "This week" }, expect.anything());
    expect(screen.getByRole("status").textContent).toContain("accepted for delivery");
  });

  it("reports a refusal in the tool's words", () => {
    mutate.mockImplementation((_input, opts: { onError: (e: Error) => void }) =>
      opts.onError(new Error('notify: no asset "a1" is available to this session')),
    );
    render(<PostToChannelDialog assetId="a1" open onOpenChange={vi.fn()} />);
    chooseChannel("ops-alerts");
    fireEvent.click(screen.getByRole("button", { name: /^post$/i }));
    expect(screen.getByRole("status").textContent).toContain("is available to this session");
  });

  it("says why nothing is offered", () => {
    channels({ channels: [], note: "this session can reach no notification channels" });
    render(<PostToChannelDialog assetId="a1" open onOpenChange={vi.fn()} />);
    expect(screen.getByTestId("no-channels").textContent).toContain("can reach no notification channels");
  });

  it("states a load failure and a load in progress", () => {
    channels(undefined, { error: new Error("503") });
    render(<PostToChannelDialog assetId="a1" open onOpenChange={vi.fn()} />);
    expect(screen.getByText(/could not read the channels/i)).toBeInTheDocument();
    cleanup();
    channels(undefined, { isLoading: true });
    render(<PostToChannelDialog assetId="a1" open onOpenChange={vi.fn()} />);
    expect(screen.getByText(/loading channels/i)).toBeInTheDocument();
  });

  it("clears what it showed when closed", () => {
    const onOpenChange = vi.fn();
    render(<PostToChannelDialog assetId="a1" open onOpenChange={onOpenChange} />);
    // The footer's Close, not the corner X the dialog adds.
    const footerClose = screen.getAllByRole("button", { name: /close/i }).find((b) => b.textContent === "Close");
    fireEvent.click(footerClose!);
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(reset).toHaveBeenCalled();
  });
});
