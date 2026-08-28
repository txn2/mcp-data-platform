import { describe, it, expect } from "vitest";
import { formatUser, parsePrincipal, principalOptions } from "./formatUser";

const OWNER = "analyst@example.com";

describe("reading a principal off a user id", () => {
  it("separates the automations from the person by the prefix the platform mints", () => {
    expect(parsePrincipal("script:acme-revenue-pulse", OWNER)).toEqual({
      kind: "script",
      name: "acme-revenue-pulse",
      email: OWNER,
    });
    expect(parsePrincipal("apikey:nightly-load", "ops@example.com")).toEqual({
      kind: "apikey",
      name: "nightly-load",
      email: "ops@example.com",
    });
    expect(parsePrincipal("a233eaf7-fd39-4e53-8086-b264c1f82d2a", OWNER).kind).toBe("user");
  });

  it("drops the synthetic address a key with no mailbox authenticates as", () => {
    expect(parsePrincipal("apikey:nightly-load", "nightly-load@apikey.local").email).toBeUndefined();
  });
});

describe("labelling a principal", () => {
  it("names a person by their address", () => {
    expect(formatUser("a233eaf7-fd39-4e53-8086-b264c1f82d2a", OWNER)).toBe(OWNER);
  });

  it("truncates the subject of a person the record carries no address for", () => {
    expect(formatUser("a233eaf7-fd39-4e53-8086-b264c1f82d2a")).toBe("a233eaf7…");
    expect(formatUser("someone")).toBe("someone");
  });

  it("names an automation by its kind and name, then whoever it acts for", () => {
    expect(formatUser("script:acme-revenue-pulse", OWNER)).toBe(
      `script: acme-revenue-pulse - ${OWNER}`,
    );
    expect(formatUser("apikey:nightly-load", "nightly-load@apikey.local")).toBe(
      "apikey: nightly-load",
    );
  });
});

describe("the user facet's options", () => {
  const USERS = [
    "a233eaf7-fd39-4e53-8086-b264c1f82d2a",
    "apikey:nightly-load",
    "script:acme-revenue-pulse",
    "script:acme-top-stores-drop",
    "script:acme-daily-payment-mix",
  ];
  const LABELS: Record<string, string> = {
    "a233eaf7-fd39-4e53-8086-b264c1f82d2a": OWNER,
    "apikey:nightly-load": "nightly-load@apikey.local",
    "script:acme-revenue-pulse": OWNER,
    "script:acme-top-stores-drop": OWNER,
    "script:acme-daily-payment-mix": OWNER,
  };

  it("gives a person and each script they own a label of its own", () => {
    const labels = principalOptions(USERS, LABELS).map((o) => o.label);

    expect(new Set(labels).size).toBe(labels.length);
    expect(labels).toContain(OWNER);
    expect(labels).toContain(`script: acme-revenue-pulse - ${OWNER}`);
  });

  it("keeps the id as the value, which is what the API filters on", () => {
    expect(principalOptions(USERS, LABELS).map((o) => o.value).sort()).toEqual([...USERS].sort());
  });

  it("orders people before the automations rather than by an id nobody reads", () => {
    expect(principalOptions(USERS, LABELS).map((o) => o.value)).toEqual([
      "a233eaf7-fd39-4e53-8086-b264c1f82d2a",
      "script:acme-daily-payment-mix",
      "script:acme-revenue-pulse",
      "script:acme-top-stores-drop",
      "apikey:nightly-load",
    ]);
  });

  it("says which subject it is when one address reaches the facet twice", () => {
    const users = ["a233eaf7-fd39-4e53-8086-b264c1f82d2a", "b8140c22-1f0e-4a77-9d31-6c2b5e0af914"];
    const labels = { [users[0]!]: OWNER, [users[1]!]: OWNER };

    const options = principalOptions(users, labels);

    expect(options.map((o) => o.label)).toEqual([
      `${OWNER} (a233eaf7…)`,
      `${OWNER} (b8140c22…)`,
    ]);
    expect(options.map((o) => o.value)).toEqual(users);
  });

  it("leaves a label alone when nothing else reads like it", () => {
    expect(principalOptions(USERS, LABELS).map((o) => o.label)).toContain(OWNER);
  });

  it("has no options before the facet has loaded", () => {
    expect(principalOptions(undefined, undefined)).toEqual([]);
  });
});
