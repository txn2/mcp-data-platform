import { describe, it, expect } from "vitest";
import type { Site } from "@/api/portal/hooks/sites";
import { cardAddresses, envAddress, envBadgeText, envOpenUrl, envTip, initials } from "./siteFormat";

// #2612: what the Sites list shows for an owner and an environment.
describe("initials", () => {
  it("takes the first letters of the first two parts of the name", () => {
    expect(initials("ana.lopez@example.com")).toBe("AL");
    expect(initials("priya_nair-smith@example.com")).toBe("PN");
    expect(initials("sam+sites@example.com")).toBe("SS");
  });

  it("takes the first two letters of a one-part name", () => {
    expect(initials("admin@example.com")).toBe("AD");
    expect(initials("j@example.com")).toBe("J");
  });

  it("says ? for an empty address", () => {
    expect(initials("")).toBe("?");
  });
});

describe("envBadgeText", () => {
  it("names the environment and the release it serves", () => {
    expect(envBadgeText({ name: "prod", release: 39 })).toBe("prod 39");
  });

  it("says when an environment has no release", () => {
    expect(envBadgeText({ name: "dev", release: 0 })).toBe("dev · no release");
  });
});

describe("envTip", () => {
  it("names the release, who can open it and since when", () => {
    expect(envTip({ name: "prod", release: 39, public_domains: ["example.com"], updated_at: "2026-11-03T12:00:00Z" })).toBe(
      "prod serves release 39, public at example.com, since 11/03/2026",
    );
  });

  it("leaves out the date it cannot read", () => {
    expect(envTip({ name: "dev", release: 4, public_domains: [], updated_at: "" })).toBe("dev serves release 4, private");
  });

  it("says an environment has no release yet", () => {
    expect(envTip({ name: "stage", release: 0, public_domains: [], updated_at: "" })).toBe("stage has no release yet, private");
  });
});

// #2607 and #2611: what each environment card lists, and where people reach an
// environment, from the addresses the server answers with.
describe("cardAddresses and envOpenUrl", () => {
  const site = (name: string, main: string, pages: Record<string, string>, addresses: Record<string, string>, domains: [string, string, boolean?][]) =>
    ({
      name, main_environment: main, address: pages[main] ?? "", pages_addresses: pages, addresses,
      domains: domains.map(([hostname, environment, isPublic]) => ({ hostname, environment, public: !!isPublic, created_by: "x", created_at: "" })),
    }) as unknown as Site;

  const docs = site(
    "docs", "dev",
    { dev: "https://docs.pages.example.net", staging: "https://docs--staging.pages.example.net", prod: "https://docs--prod.pages.example.net" },
    { dev: "https://docs.pages.example.net", staging: "https://docs--staging.pages.example.net", prod: "https://docs.example.com" },
    [["www.docs.example.com", "prod"], ["docs.example.com", "prod", true]],
  );

  it("lists a public domain first, then the private ones, then its pages address, which is always private (#2619)", () => {
    expect(cardAddresses(docs, "prod")).toEqual([
      { href: "https://docs.example.com", text: "docs.example.com", tag: "Domain", public: true },
      { href: "https://www.docs.example.com", text: "www.docs.example.com", tag: "Domain", public: false },
      { href: "https://docs--prod.pages.example.net", text: "docs--prod.pages.example.net", tag: "Pages address", public: false },
    ]);
  });

  it("tags the main address as the main address", () => {
    expect(cardAddresses(docs, "dev")).toEqual([{ href: "https://docs.pages.example.net", text: "docs.pages.example.net", tag: "Main address", public: false }]);
  });

  it("lists only the domains of a two-character site's environment, and nothing where it has none", () => {
    const ab = site("ab", "dev", { dev: "https://ab.pages.example.net", staging: "", prod: "" }, { dev: "https://ab.pages.example.net", staging: "", prod: "https://ab.example.com" }, [["ab.example.com", "prod"]]);
    expect(cardAddresses(ab, "prod")).toEqual([{ href: "https://ab.example.com", text: "ab.example.com", tag: "Domain", public: false }]);
    expect(cardAddresses(ab, "staging")).toEqual([]);
  });

  it("reaches an environment at its domain first, as manage_site get answers", () => {
    expect(envOpenUrl(docs, "prod")).toBe("https://docs.example.com");
    expect(envOpenUrl(docs, "staging")).toBe("https://docs--staging.pages.example.net");
    expect(envAddress(docs, "prod")).toBe("https://docs--prod.pages.example.net");
    expect(envOpenUrl(docs, "archive")).toBe("");
  });
});
