import { describe, it, expect } from "vitest";
import type { Share } from "@/api/portal/types";
import { accessEnds, generalMode, groupShares, guestNote, highlight, initials, isGeneralLink, opened, sharedWith, tintFor, TINTS } from "./model";
import { postedWhere } from "./PostedToChannels";
import { destinations } from "./ChannelView";

const NOW = Date.parse("2026-10-25T12:00:00Z");

function share(over: Partial<Share>): Share {
  return {
    id: "s", asset_id: "a", token: "t", created_by: "o@example.com", permission: "viewer", access_mode: "authenticated",
    revoked: false, access_count: 0, created_at: "2026-10-01T12:00:00Z", ...over,
  };
}

describe("reading an item's shares (#2291)", () => {
  it("is the server's rule for the general link", () => {
    expect(isGeneralLink(share({}), NOW)).toBe(true);
    expect(isGeneralLink(share({ shared_with_email: "a@example.com" }), NOW)).toBe(false);
    expect(isGeneralLink(share({ origin: "channel_post" }), NOW)).toBe(false);
    expect(isGeneralLink(share({ posted_to: "#x (team)" }), NOW)).toBe(false);
    expect(isGeneralLink(share({ revoked: true }), NOW)).toBe(false);
    expect(isGeneralLink(share({ access_mode: "public", expires_at: "2026-10-24T00:00:00Z" }), NOW)).toBe(false);
  });

  it("sorts grants oldest first, keeps the newest link, and lists the others and the posts apart", () => {
    const g = groupShares(
      [
        share({ id: "g2", shared_with_email: "b@example.com", created_at: "2026-10-03T00:00:00Z" }),
        share({ id: "g1", shared_with_persona: "finance", created_at: "2026-10-02T00:00:00Z" }),
        share({ id: "l-old", created_at: "2026-09-01T00:00:00Z" }),
        share({ id: "l-new", created_at: "2026-10-01T00:00:00Z" }),
        share({ id: "p", origin: "channel_post", posted_to: "#general (team)" }),
        share({ id: "gone", shared_with_email: "c@example.com", revoked: true }),
      ],
      NOW,
    );
    expect(g.grants.map((s) => s.id)).toEqual(["g1", "g2"]);
    expect(g.link?.id).toBe("l-new");
    expect(g.otherLinks.map((s) => s.id)).toEqual(["l-old"]);
    expect(g.posts.map((s) => s.id)).toEqual(["p"]);
    expect(generalMode(g.link)).toBe("authenticated");
    expect(generalMode(share({ access_mode: "public" }))).toBe("public");
    expect(generalMode(undefined)).toBe("none");
  });
});

describe("the dialog's words (#2291)", () => {
  it("says how long access lasts only when it ends", () => {
    expect(accessEnds(undefined, NOW)).toBe("");
    expect(accessEnds("2026-10-28T13:00:00Z", NOW)).toBe("Access ends in 3 days");
    expect(accessEnds("2026-10-26T11:00:00Z", NOW)).toBe("Access ends in 23 hours");
    expect(accessEnds("2026-10-25T12:30:00Z", NOW)).toBe("Access ends within the hour");
    expect(accessEnds("2026-10-25T11:00:00Z", NOW)).toBe("Access has ended");
  });

  it("counts opens and recipients the way a person says them", () => {
    expect(opened(0)).toBe("");
    expect(opened(1)).toBe("opened once");
    expect(opened(4)).toBe("opened 4 times");
    expect(sharedWith(["Ann"])).toBe("Shared with Ann");
    expect(sharedWith(["Ann", "Bo"])).toBe("Shared with Ann and 1 other");
    expect(sharedWith(["Ann", "Bo", "Cy"])).toBe("Shared with Ann and 2 others");
  });

  it("tells a guest's sharer what the guest gets, a file's differently", () => {
    expect(guestNote(["d@x.org"], false)).toBe(
      "d@x.org doesn’t have an account. They’ll get an email with a link to view it. Guests can view but not edit.",
    );
    expect(guestNote(["d@x.org", "e@x.org"], true)).toMatch(/^2 people don’t have an account\. They’ll get an email, and can open it once they have an account\./);
  });

  it("marks a person by their initials in a tint that stays theirs", () => {
    expect(initials("Fiona May Field", "f@example.com")).toBe("FF");
    expect(initials("Fiona", "f@example.com")).toBe("F");
    expect(initials("", "dana@example.org")).toBe("D");
    expect(tintFor("fiona@example.com")).toBe(tintFor("fiona@example.com"));
    expect(TINTS).toContain(tintFor("anyone"));
    expect(highlight("Finance", "fin")).toEqual(["", "Fin", "ance"]);
    expect(highlight("Finance", "zz")).toEqual(["Finance", "", ""]);
  });
});

describe("where a post went (#2291)", () => {
  const channels = [
    { name: "team", kind: "slack", mode: "", carries: "", targets: [{ id: "C1", name: "general" }] },
    { name: "ops-mail", kind: "email", mode: "", carries: "", targets: [] },
    { name: "hooks", kind: "webhook", mode: "", carries: "", targets: [] },
  ];

  it("reads an asset's post, a file's, and a channel's own", () => {
    expect(postedWhere("#general (team)", channels)).toEqual({ channel: "#general", kind: "Slack" });
    expect(postedWhere("C1 on team", channels)).toEqual({ channel: "#general", kind: "Slack" });
    expect(postedWhere("ops-mail", channels)).toEqual({ channel: "ops-mail", kind: "Email" });
    expect(postedWhere("hooks", channels)).toEqual({ channel: "#hooks", kind: "Webhook" });
    expect(postedWhere("gone", channels)).toEqual({ channel: "#gone", kind: "" });
  });

  it("offers a bot whose chat channels could not be read, disabled with why", () => {
    const out = destinations([
      ...channels,
      { name: "mm", kind: "mattermost", mode: "", carries: "", targets: [], targets_error: "token expired" },
      { name: "silent", kind: "slack", mode: "", carries: "", targets: [] },
    ]);
    expect(out.map((d) => `${d.label} · ${d.kind}`)).toEqual(["ops-mail · Email", "mm · Mattermost", "#general · Slack", "#hooks · Webhook"]);
    expect(out.find((d) => d.channel === "mm")?.unavailable).toBe("token expired");
  });
});
