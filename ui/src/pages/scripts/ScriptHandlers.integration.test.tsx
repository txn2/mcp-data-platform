import { describe, it, expect } from "vitest";
import type { HandlerDetail } from "@/api/portal/hooks/scriptHandlers";
import { handlerTarget } from "./ScriptHandlers";

// #2659: a script chosen for an integration lists it among its handlers.
describe("an integration on a script's Handlers section (#2659)", () => {
  it("names the operation", () => {
    const h: HandlerDetail = { handler: "b1", kind: "integration", type: "integration", name: "ingress-routes", enabled: true, operation: "site_routing" };
    expect(handlerTarget(h)).toBe("Site routing");
    expect(handlerTarget({ ...h, operation: "later_op" })).toBe("later_op");
  });
});
