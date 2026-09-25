import { describe, expect, it } from "vitest";
import { mockWebhookSources } from "@/mocks/data/webhooks";
import { EMPTY_FORM, fromSource, problems, senderURL, toInput, windowLength } from "./webhookForm";

const hmac = mockWebhookSources[0]!;
const token = mockWebhookSources[1]!;

describe("webhook form", () => {
  it("never reads a secret back, so an edit keeps the stored one", () => {
    const form = fromSource(hmac);
    expect(form.secret).toBe("");
    const body = toInput(form, false);
    expect(body.auth.secret).toBeUndefined();
    expect(body.rotation_overlap_seconds).toBeUndefined();
    expect(body.name).toBeUndefined();
    expect(body.connection).toBeUndefined();
    expect(body.config.persona).toBeUndefined();
  });

  it("round-trips a source's settings", () => {
    const body = toInput(fromSource(hmac), false);
    expect(body.auth).toMatchObject({
      mode: "hmac",
      signature_header: "X-Signature",
      prefix: "sha256=",
      timestamp_header: "X-Timestamp",
      tolerance_seconds: 300,
      signed: "timestamp.body",
    });
    expect(body.config).toMatchObject({ split: "$", event_id_path: "$.sg_event_id", buffer_limit: 50000 });
    expect(toInput(fromSource(token), false).config.compacted_retention_days).toBe(0);
  });

  it("carries the compaction window, an hour unless the source set one", () => {
    expect(toInput(fromSource(hmac), false).config.compact_every_minutes).toBe(15);
    expect(toInput(fromSource(token), false).config.compact_every_minutes).toBe(60);
    expect(toInput({ ...EMPTY_FORM, compactEveryMinutes: "1" }, true).config.compact_every_minutes).toBe(1);
    expect([windowLength(60), windowLength(1), windowLength(5)]).toEqual(["Hour", "Minute", "5 minutes"]);
  });

  it("rotates with the overlap a new secret is sent with", () => {
    const form = { ...fromSource(hmac), secret: "whsec_new", rotationOverlapHours: "2" };
    const body = toInput(form, false);
    expect(body.auth.secret).toBe("whsec_new");
    expect(body.rotation_overlap_seconds).toBe(7200);
  });

  it("sends only the settings the chosen mode reads", () => {
    const form = { ...EMPTY_FORM, mode: "header_token" as const, header: "X-Token", signatureHeader: "X-Sig" };
    expect(toInput(form, true).auth).toEqual({ mode: "header_token", secret: undefined, header: "X-Token" });
    expect(toInput({ ...EMPTY_FORM, mode: "basic", username: "u" }, true).auth).toEqual({
      mode: "basic",
      secret: undefined,
      username: "u",
    });
    expect(toInput({ ...EMPTY_FORM, mode: "path_token" }, true).auth).toEqual({ mode: "path_token", secret: undefined });
    const noTimestamp = toInput({ ...EMPTY_FORM, signatureHeader: "X", signed: "timestamp.body" }, true);
    expect(noTimestamp.auth.signed).toBe("body");
  });

  it("sends a number only when one was entered", () => {
    const body = toInput({ ...EMPTY_FORM, bufferLimit: " 100 ", maxBodyBytes: "abc" }, true);
    expect(body.config.buffer_limit).toBe(100);
    expect(body.config.max_body_bytes).toBeUndefined();
    expect(body.config.flush_max_events).toBeUndefined();
  });

  it("names what stops a new source being sent", () => {
    expect(problems(EMPTY_FORM, true)).toEqual([
      "The name starts with a lowercase letter and holds only lowercase letters, digits and -.",
      "Choose the connection the source's table is created on.",
      "Enter the secret the sender uses.",
      "Name the header the signature is sent in.",
    ]);
    expect(problems({ ...EMPTY_FORM, name: "new", connection: "c", secret: "s", signatureHeader: "X" }, true)).toEqual([
      "The name new is reserved.",
    ]);
    const ready = { ...EMPTY_FORM, name: "esp", connection: "c", secret: "s", signatureHeader: "X" };
    expect(problems(ready, true)).toEqual([]);
    expect(problems({ ...ready, mode: "header_token" }, true)).toEqual(["Name the header the token is sent in."]);
    expect(problems({ ...ready, mode: "basic" }, true)).toEqual(["Enter the username."]);
    expect(problems({ ...fromSource(hmac) }, false)).toEqual([]);
  });

  it("shows the address to give the sender", () => {
    expect(senderURL("https://platform.example.com/", hmac)).toBe("https://platform.example.com/hooks/email-events");
    const pathToken = { ...hmac, auth: { ...hmac.auth, mode: "path_token" as const } };
    expect(senderURL("https://platform.example.com", pathToken)).toBe(
      "https://platform.example.com/hooks/email-events/<token>",
    );
  });
});
