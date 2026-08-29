import { describe, it, expect } from "vitest";
import type { UserProfile } from "@/stores/auth";
import { scopeIcon, scopeLabel } from "./shared";

// How a library is named to the person reading it. A user library is only "My
// Resources" to the person it belongs to, and the administrator's section lists
// every library at once (#1502).

const viewer = (id: string): UserProfile => ({ user_id: id }) as UserProfile;

describe("naming the library a resource is filed in", () => {
  it("names the global library and a persona by what they are", () => {
    expect(scopeLabel("global", "", viewer("sub-1"))).toBe("Global");
    expect(scopeLabel("persona", "analyst", viewer("sub-1"))).toBe("analyst");
  });

  it("names the reader's own library as theirs", () => {
    expect(scopeLabel("user", "sub-1", viewer("sub-1"))).toBe("My Resources");
  });

  // A raw subject identifier names nobody, so a library keyed by one is
  // described rather than printed.
  it("names somebody else's library by address, and describes one keyed by an identifier", () => {
    expect(scopeLabel("user", "her@example.com", viewer("sub-1"))).toBe("her@example.com's library");
    expect(scopeLabel("user", "sub-9", viewer("sub-1"))).toBe("Another person's library");
  });

  it("leaves a user library undecided with nobody signed in", () => {
    expect(scopeLabel("user", "sub-9", null)).toBe("My Resources");
  });
});

describe("the icon a library wears", () => {
  it("gives each scope its own", () => {
    const icons = ["global", "persona", "user"].map(scopeIcon);
    expect(new Set(icons).size).toBe(3);
  });
});
