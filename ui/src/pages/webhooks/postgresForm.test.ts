import { describe, expect, it } from "vitest";
import type { WebhookSource } from "@/api/admin/types";
import { EMPTY_FORM, fromSource, problems, senderURL, toInput, type WebhookForm } from "./webhookForm";
import { EMPTY_POSTGRES, fromPostgres, postgresOf, postgresProblems } from "./postgresForm";

const outboxForm: WebhookForm = {
  ...EMPTY_FORM,
  input: "postgres",
  name: "orders-outbox",
  connection: "scratch",
  postgres: { ...EMPTY_POSTGRES, connection: "orders", table: "public.outbox", pollSeconds: "10" },
};

describe("postgres source form (#2408)", () => {
  it("sends the database, no auth and no secret", () => {
    const body = toInput(outboxForm, true);
    expect(body.auth).toEqual({ mode: "" });
    expect(body.config.input).toBe("postgres");
    expect(body.config.postgres).toEqual({ connection: "orders", mode: "outbox", table: "public.outbox", poll_seconds: 10, max_payload_bytes: undefined });
    expect(body.config.handshake).toBeUndefined();
    expect(problems(outboxForm, true)).toEqual([]);
  });

  it("sends only what notify reads", () => {
    const f = { ...outboxForm, postgres: { ...outboxForm.postgres, mode: "notify" as const, channels: "order_events\n\n refunds " } };
    expect(postgresOf(f.postgres)).toEqual({ connection: "orders", mode: "notify", channels: ["order_events", "refunds"], max_payload_bytes: undefined });
  });

  it("names what is missing before a round trip", () => {
    expect(postgresProblems(EMPTY_POSTGRES)).toEqual(["Choose the application database the source reads."]);
    expect(postgresProblems({ ...EMPTY_POSTGRES, connection: "orders", table: "Outbox" })).toEqual([
      "Enter the outbox as schema.table, in lowercase.",
    ]);
    expect(postgresProblems({ ...EMPTY_POSTGRES, connection: "orders", mode: "notify" })).toEqual([
      "Name at least one channel to listen on.",
    ]);
  });

  it("round-trips a stored source and has no sender address", () => {
    const stored = {
      name: "orders-outbox", enabled: true, connection: "scratch", path: "", table: "webhook_orders_outbox",
      auth: { mode: "" }, created_by: "", created_at: "", updated_at: "",
      config: { input: "postgres", postgres: { connection: "orders", mode: "notify", channels: ["a", "b"], max_payload_bytes: 1024 } },
    } as unknown as WebhookSource;
    const f = fromSource(stored);
    expect(f.input).toBe("postgres");
    expect(f.postgres).toEqual({ connection: "orders", mode: "notify", channels: "a\nb", table: "", pollSeconds: "", maxPayloadBytes: "1024" });
    expect(toInput(f, false).config.input).toBe("postgres");
    expect(senderURL("https://p.example.com", stored)).toBe("");
    expect(fromPostgres(undefined)).toEqual(EMPTY_POSTGRES);
  });
});
