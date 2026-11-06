import { describe, it, expect } from "vitest";
import { threadHeading } from "./meta";

// #2513: the line a thread is listed by.
describe("threadHeading", () => {
  it("prefers a stored title", () => {
    expect(threadHeading({ kind: "comment", title: "Old subject", first_message: "Body" })).toBe("Old subject");
  });
  it("uses the first message when there is no title", () => {
    expect(threadHeading({ kind: "comment", title: "  ", first_message: " Body line " })).toBe("Body line");
  });
  it("names the kind when there is neither", () => {
    expect(threadHeading({ kind: "rating" })).toBe("Rating");
  });
});
