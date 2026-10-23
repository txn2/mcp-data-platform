import { describe, expect, it } from "vitest";
import { mockWebhookSources } from "@/mocks/data/webhooks";
import { EMPTY_IMAP, fromIMAP, imapProblems, lines } from "./imapForm";
import { EMPTY_FORM, fromSource, problems, senderURL, toInput } from "./webhookForm";

const mailbox = mockWebhookSources.find((s) => s.config.input === "imap")!;

describe("mailbox form (#2247)", () => {
  it("round-trips a mailbox source and sends nothing about requests", () => {
    const form = fromSource(mailbox);
    expect(form.input).toBe("imap");
    expect(form.imap.folders).toBe("INBOX");
    const body = toInput(form, false);
    expect(body.auth).toEqual({ mode: "imap", secret: undefined, username: "platform@acme.example" });
    expect(body.config.input).toBe("imap");
    expect(body.config.imap).toEqual({
      host: "imap.acme.example",
      port: 993,
      tls: "tls",
      folders: ["INBOX"],
      allow_senders: ["no-reply@zoom.example"],
      require_auth_pass: true,
      trusted_authserv_id: "mx.acme.example",
      tls_ca_bundle_pem: undefined,
      poll_seconds: 60,
      max_message_bytes: 10485760,
      max_attachment_bytes: 26214400,
    });
    expect(body.config.compact_every_minutes).toBe(60);
    expect(body.config.compacted_retention_days).toBe(400);
    expect(body.config).not.toHaveProperty("split");
    expect(body.config).not.toHaveProperty("max_body_bytes");
    expect(body.config).not.toHaveProperty("handshake");
  });

  it("replaces the password without a rotation overlap", () => {
    const body = toInput({ ...fromSource(mailbox), secret: "new-app-password" }, false);
    expect(body.auth.secret).toBe("new-app-password");
    expect(body.rotation_overlap_seconds).toBeUndefined();
  });

  it("names what a new mailbox source is missing", () => {
    const form = { ...EMPTY_FORM, input: "imap" as const, name: "minutes", connection: "c", imap: { ...EMPTY_IMAP, folders: "" } };
    expect(problems(form, true)).toEqual([
      "Enter the mailbox's password.",
      "Enter the mail server's host.",
      "Enter the mailbox's username.",
      "Name at least one folder to read.",
      "Name the addresses or @domains whose mail becomes events.",
      "Enter the receiving server's id, or stop requiring an authenticated sender.",
    ]);
    const ready = {
      ...form,
      secret: "pw",
      username: "platform",
      imap: { ...EMPTY_IMAP, host: "imap.example.com", allowSenders: "@example.com", requireAuthPass: false },
    };
    expect(problems(ready, true)).toEqual([]);
    expect(toInput(ready, true).config.imap?.trusted_authserv_id).toBeUndefined();
  });

  it("reads list fields one entry per line", () => {
    expect(lines(" INBOX \n\nMeetings\n")).toEqual(["INBOX", "Meetings"]);
    expect(fromIMAP(undefined)).toBe(EMPTY_IMAP);
    expect(imapProblems({ ...EMPTY_IMAP, host: "h", allowSenders: "a@b.example", trustedAuthServID: "mx" }, "u")).toEqual([]);
  });

  it("gives no sender address for a mailbox", () => {
    expect(senderURL("https://platform.example", mailbox)).toBe("");
  });
});
