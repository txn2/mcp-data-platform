import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { IMAPConfigForm, listText, listValue } from "./IMAPConfigForm";
import { CalDAVConfigForm } from "./CalDAVConfigForm";

afterEach(cleanup);

describe("list fields", () => {
  it("reads a list or a comma string one entry per line, and writes one back", () => {
    expect(listText(["Platform", "Vendors"])).toBe("Platform\nVendors");
    expect(listText("a, b,,c")).toBe("a\nb\nc");
    expect(listText(undefined)).toBe("");
    expect(listValue("@vendor.example\n ap@other.example \n\n")).toEqual(["@vendor.example", "ap@other.example"]);
    expect(listValue("  ")).toBeUndefined();
  });
});

describe("IMAPConfigForm", () => {
  it("writes the folders and senders as lists and hides the server id with authentication off", () => {
    const onChange = vi.fn();
    const { rerender } = render(<IMAPConfigForm config={{ host: "imap.example.com" }} onChange={onChange} />);
    fireEvent.change(screen.getByLabelText(/Folders/), { target: { value: "Platform\nVendors" } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ folders: ["Platform", "Vendors"] }));
    fireEvent.change(screen.getByLabelText(/Allowed senders/), { target: { value: "@vendor.example" } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ allow_senders: ["@vendor.example"] }));
    expect(screen.getByLabelText(/Receiving server's id/)).toBeInTheDocument();
    expect(screen.getByPlaceholderText("993")).toBeInTheDocument();

    rerender(<IMAPConfigForm config={{ require_auth_pass: false, tls: "starttls" }} onChange={onChange} />);
    expect(screen.queryByLabelText(/Receiving server's id/)).toBeNull();
    expect(screen.getByPlaceholderText("143")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Max results"), { target: { value: "20" } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ max_results: 20 }));
  });
});

describe("CalDAVConfigForm", () => {
  it("writes the account address and the zone", () => {
    const onChange = vi.fn();
    render(<CalDAVConfigForm config={{}} onChange={onChange} connectionName="team" isCreate />);
    fireEvent.change(screen.getByLabelText(/Account URL/), { target: { value: "https://cal.example.com/" } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ url: "https://cal.example.com/" }));
    fireEvent.change(screen.getByLabelText(/Time zone/), { target: { value: "Europe/Berlin" } });
    expect(onChange).toHaveBeenLastCalledWith(expect.objectContaining({ time_zone: "Europe/Berlin" }));
  });
});
