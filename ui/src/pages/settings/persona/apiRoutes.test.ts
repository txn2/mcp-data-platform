import { describe, it, expect } from "vitest";
import type { APIRouteRule } from "@/api/admin/types";
import {
  operationKey,
  parseOperationKey,
  resolveRoute,
  ruleForOperation,
  ruleGoverns,
  withOperationRule,
  withoutRuleAt,
} from "./apiRoutes";

// The editor's preview of a persona's API route rules mirrors
// pkg/persona/filter.go WhyAPIRouteAllowed. The cases below are the ones that
// distinguish it from a plain two-bucket pattern match (#1479).

const ORDER_PATH = "/v1/orders/{id}";

const deleteOrder = { method: "DELETE", path: ORDER_PATH };
const listOrders = { method: "GET", path: "/v1/orders" };

describe("resolveRoute", () => {
  it("leaves a connection no rule names open", () => {
    const rules: APIRouteRule[] = [{ connection: "billing", action: "deny" }];
    expect(resolveRoute(rules, "crm", "DELETE", ORDER_PATH).decision).toBe("open");
  });

  it("treats an empty rule list as open, not as a denial", () => {
    // The distinction the whole scope rests on: connections are
    // deny-by-default, API routes are not, and showing an unruled operation as
    // denied would tell an operator to grant back access they already have.
    expect(resolveRoute([], "crm", "DELETE", ORDER_PATH).decision).toBe("open");
  });

  it("denies when a deny rule matches, and names the rule", () => {
    const rules: APIRouteRule[] = [
      { connection: "crm-*", action: "allow" },
      { connection: "crm-*", methods: ["DELETE"], paths: [ORDER_PATH], action: "deny" },
    ];
    const r = resolveRoute(rules, "crm-prod", "DELETE", ORDER_PATH);
    expect(r.decision).toBe("deny");
    expect(r.rule?.paths).toEqual([ORDER_PATH]);
    expect(r.index).toBe(1);
  });

  it("puts deny ahead of an allow that also matches", () => {
    const rules: APIRouteRule[] = [
      { connection: "crm", methods: ["DELETE"], action: "deny" },
      { connection: "crm", action: "allow" },
    ];
    expect(resolveRoute(rules, "crm", "DELETE", ORDER_PATH).decision).toBe("deny");
  });

  it("denies with no rule to point at when the set admits nothing", () => {
    // Once any rule names a connection, an operation must match an allow rule.
    // No single rule produced this denial, and the rail has to say so.
    const rules: APIRouteRule[] = [{ connection: "crm", methods: ["GET"] }];
    const r = resolveRoute(rules, "crm", "DELETE", ORDER_PATH);
    expect(r.decision).toBe("deny");
    expect(r.rule).toBeUndefined();
  });

  it("reads an omitted action as an allow", () => {
    const rules: APIRouteRule[] = [{ connection: "crm", methods: ["DELETE"] }];
    expect(resolveRoute(rules, "crm", "DELETE", ORDER_PATH).decision).toBe("allow");
  });

  it("reads empty methods and paths as any", () => {
    const rules: APIRouteRule[] = [{ connection: "crm" }];
    expect(resolveRoute(rules, "crm", "PATCH", "/anything").decision).toBe("allow");
  });

  it("does not let a single-segment glob reach a deeper path", () => {
    // filepath.Match's `*` does not cross a separator, and the preview must
    // agree or it would show an operation as governed that the server leaves
    // untouched.
    const rules: APIRouteRule[] = [{ connection: "crm", paths: ["/v1/orders/*"] }];
    expect(resolveRoute(rules, "crm", "GET", "/v1/orders/42").decision).toBe("allow");
    expect(resolveRoute(rules, "crm", "GET", "/v1/orders/42/items").decision).toBe(
      "deny",
    );
  });

  it("skips a rule with no connection", () => {
    const rules: APIRouteRule[] = [{ connection: "", action: "deny" }];
    expect(resolveRoute(rules, "crm", "GET", "/v1/orders").decision).toBe("open");
  });
});

describe("ruleForOperation", () => {
  it("names the operation's own method and declared path", () => {
    expect(ruleForOperation("crm", deleteOrder, "deny")).toEqual({
      connection: "crm",
      methods: ["DELETE"],
      paths: [ORDER_PATH],
      action: "deny",
    });
  });

  it("uppercases the method the way the server matches it", () => {
    expect(
      ruleForOperation("crm", { method: "delete", path: ORDER_PATH }, "deny").methods,
    ).toEqual(["DELETE"]);
  });
});

describe("withOperationRule", () => {
  it("denies exactly the selected operation and leaves its siblings alone", () => {
    const rules = withOperationRule([{ connection: "crm" }], "crm", deleteOrder, "deny");
    expect(resolveRoute(rules, "crm", "DELETE", ORDER_PATH).decision).toBe("deny");
    expect(resolveRoute(rules, "crm", "GET", listOrders.path).decision).toBe("allow");
    expect(resolveRoute(rules, "crm", "GET", ORDER_PATH).decision).toBe("allow");
  });

  it("flips a decision rather than stacking a contradictory rule under it", () => {
    const denied = withOperationRule([], "crm", deleteOrder, "deny");
    const allowed = withOperationRule(denied, "crm", deleteOrder, "allow");
    expect(allowed).toHaveLength(1);
    expect(allowed[0]?.action).toBe("allow");
  });

  it("leaves a broader hand-written rule in place", () => {
    // The operator's own pattern is theirs. Rewriting it into a selection is
    // exactly what must not happen when a persona is saved from the editor.
    const handWritten: APIRouteRule = {
      connection: "crm",
      paths: ["/v1/admin/*"],
      action: "deny",
    };
    const next = withOperationRule([handWritten], "crm", deleteOrder, "deny");
    expect(next[0]).toEqual(handWritten);
    expect(next).toHaveLength(2);
  });
});

describe("withoutRuleAt", () => {
  it("removes one rule by position, keeping identically written siblings", () => {
    const twin: APIRouteRule = { connection: "crm", action: "deny" };
    const next = withoutRuleAt([twin, { ...twin }], 0);
    expect(next).toHaveLength(1);
  });
});

describe("ruleGoverns", () => {
  it("marks every operation a rule reaches, not only the ones it decides", () => {
    const rule: APIRouteRule = { connection: "crm-*", methods: ["GET"] };
    expect(ruleGoverns(rule, "crm-prod", "GET", "/v1/orders")).toBe(true);
    expect(ruleGoverns(rule, "crm-prod", "DELETE", "/v1/orders")).toBe(false);
    expect(ruleGoverns(rule, "billing", "GET", "/v1/orders")).toBe(false);
  });
});

describe("operationKey", () => {
  it("round-trips through parseOperationKey", () => {
    const key = operationKey("acme-crm", { method: "delete", path: ORDER_PATH });
    expect(parseOperationKey(key)).toEqual({
      connection: "acme-crm",
      method: "DELETE",
      path: ORDER_PATH,
    });
  });

  it("keeps a path that carries a space whole", () => {
    const key = operationKey("crm", { method: "GET", path: "/v1/a b" });
    expect(parseOperationKey(key)?.path).toBe("/v1/a b");
  });

  it("rejects a key that names no operation", () => {
    expect(parseOperationKey("crm")).toBeNull();
    expect(parseOperationKey("crm GET")).toBeNull();
  });
});
