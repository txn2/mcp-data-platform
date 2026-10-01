import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";

import { HMACAuthFields } from "./HMACAuthFields";

// The signing block under auth_mode=hmac (#1996). A preset fills a receiver's
// whole convention and an empty field shows the preset's value, so the form
// has to say what will be sent without writing the preset's values into the
// connection, and has to hide what a Stripe header fixes itself.
describe("HMACAuthFields", () => {
  it("shows a preset's values as the placeholders of the fields it fills", () => {
    render(<HMACAuthFields config={{ hmac_preset: "standard_webhooks" }} onChange={vi.fn()} />);

    expect(screen.getByLabelText("Signature header")).toHaveAttribute("placeholder", "webhook-signature");
    expect(screen.getByLabelText("Timestamp header")).toHaveAttribute("placeholder", "webhook-timestamp");
    expect(screen.getByLabelText("Delivery ID header")).toHaveAttribute("placeholder", "webhook-id");
    expect(screen.getByLabelText("Prefix")).toHaveAttribute("placeholder", "v1,");
  });

  it("hides the prefix, the signed content and the timestamp under the Stripe format", () => {
    render(<HMACAuthFields config={{ hmac_preset: "stripe" }} onChange={vi.fn()} />);

    expect(screen.getByLabelText("Signature header")).toHaveAttribute("placeholder", "Stripe-Signature");
    expect(screen.queryByLabelText("Prefix")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Timestamp header")).not.toBeInTheDocument();
  });

  it("asks for no timestamp when only the body is signed", () => {
    render(<HMACAuthFields config={{}} onChange={vi.fn()} />);

    expect(screen.getByLabelText("Signature header")).toHaveAttribute("placeholder", "X-Signature");
    expect(screen.queryByLabelText("Timestamp header")).not.toBeInTheDocument();
  });

  it("writes what is typed under the key the server reads", () => {
    const onChange = vi.fn();
    render(<HMACAuthFields config={{ hmac_preset: "github" }} onChange={onChange} />);

    fireEvent.change(screen.getByLabelText("Signature header"), { target: { value: "X-Sig" } });
    expect(onChange).toHaveBeenLastCalledWith({ hmac_preset: "github", hmac_signature_header: "X-Sig" });
    fireEvent.change(screen.getByLabelText("Signing secret"), { target: { value: "s3cret" } });
    expect(onChange).toHaveBeenLastCalledWith({ hmac_preset: "github", credential: "s3cret" });
    fireEvent.change(screen.getByLabelText("Delivery ID header"), { target: { value: "X-Id" } });
    expect(onChange).toHaveBeenLastCalledWith({ hmac_preset: "github", hmac_id_header: "X-Id" });
  });
});
