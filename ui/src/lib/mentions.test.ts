import { describe, it, expect } from "vitest";
import {
  activeMentionQuery,
  formatMention,
  replaceMentionQuery,
  scanMentions,
  splitBody,
} from "./mentions";

describe("scanMentions", () => {
  it.each([
    ["@marcus.johnson(example.com) please review", ["marcus.johnson@example.com"]],
    ["over to @bob(example.com). thanks", ["bob@example.com"]],
    ["@bob(example.com) and @alice(example.org)", ["bob@example.com", "alice@example.org"]],
    ["@Bob(Example.com)", ["bob@example.com"]],
    ["reach me at bob@example.com", []],
    ["bob@example.com@carol(example.com)", []],
    ["@ops(mail.data-team.example.com)", ["ops@mail.data-team.example.com"]],
    ["@bob(localhost)", []],
    ["(cc @bob(example.com))", ["bob@example.com"]],
  ])("scans %s", (body, want) => {
    expect(scanMentions(body).map((m) => m.email)).toEqual(want);
  });

  it("reports the span of the token so it can be replaced in place", () => {
    const [m] = scanMentions("hi @bob(example.com)!");
    expect(m).toBeDefined();
    expect([m!.start, m!.end]).toEqual([3, 20]);
    expect(m!.raw).toBe("@bob(example.com)");
  });
});

describe("formatMention", () => {
  it.each([
    ["marcus.johnson@example.com", "@marcus.johnson(example.com)"],
    ["  bob@example.com  ", "@bob(example.com)"],
    ["bob", ""],
    ["@example.com", ""],
    ["bob@", ""],
    ["bob@example.com@evil.example", ""],
    ["bob@localhost", ""],
    ["bob smith@example.com", ""],
  ])("formats %s", (email, want) => {
    expect(formatMention(email)).toBe(want);
  });

  // The composer inserts what formatMention emits and the server parses it back
  // with the same grammar, so a token that does not survive a round trip would
  // silently address nobody.
  it("round-trips through scanMentions", () => {
    for (const email of ["bob@example.com", "first+tag@mail.example.co.uk"]) {
      expect(scanMentions(`hi ${formatMention(email)}, thanks`).map((m) => m.email)).toEqual([email]);
    }
  });
});

describe("splitBody", () => {
  it("separates mentions from the surrounding text", () => {
    expect(splitBody("hi @bob(example.com), see line 3")).toEqual([
      { kind: "text", text: "hi " },
      { kind: "mention", text: "@bob(example.com)", email: "bob@example.com" },
      { kind: "text", text: ", see line 3" },
    ]);
  });

  it("returns one text run when nothing is mentioned", () => {
    expect(splitBody("looks good")).toEqual([{ kind: "text", text: "looks good" }]);
  });

  it("handles a body that is only a mention", () => {
    expect(splitBody("@bob(example.com)")).toEqual([
      { kind: "mention", text: "@bob(example.com)", email: "bob@example.com" },
    ]);
  });
});

describe("activeMentionQuery", () => {
  it("opens on an @ at the start of the text", () => {
    expect(activeMentionQuery("@mar", 4)).toEqual({ query: "mar", start: 0 });
  });

  it("opens on an @ after whitespace or a bracket", () => {
    expect(activeMentionQuery("cc @mar", 7)).toEqual({ query: "mar", start: 3 });
    expect(activeMentionQuery("(@mar", 5)).toEqual({ query: "mar", start: 1 });
  });

  it("does not open inside an email address", () => {
    expect(activeMentionQuery("bob@exa", 7)).toBeNull();
  });

  it("closes once the token is finished or the caret moves past whitespace", () => {
    expect(activeMentionQuery("@bob(example.com) ", 18)).toBeNull();
    expect(activeMentionQuery("@bob smith", 10)).toBeNull();
  });

  it("is null with no @ before the caret", () => {
    expect(activeMentionQuery("looks good", 10)).toBeNull();
  });
});

describe("replaceMentionQuery", () => {
  it("replaces the typed fragment with a token and a trailing space", () => {
    const active = activeMentionQuery("cc @mar", 7)!;
    expect(replaceMentionQuery("cc @mar", active, "marcus@example.com")).toEqual({
      text: "cc @marcus(example.com) ",
      caret: 24,
    });
  });

  it("keeps the text that follows the fragment", () => {
    const active = activeMentionQuery("cc @mar please", 7)!;
    expect(replaceMentionQuery("cc @mar please", active, "marcus@example.com").text).toBe(
      "cc @marcus(example.com) please",
    );
  });

  it("leaves the text alone for an address that cannot be a token", () => {
    const active = activeMentionQuery("cc @mar", 7)!;
    expect(replaceMentionQuery("cc @mar", active, "not-an-address").text).toBe("cc @mar");
  });
});
