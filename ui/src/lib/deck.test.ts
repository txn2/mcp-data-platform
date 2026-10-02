import { describe, expect, it } from "vitest";
import { isSlideDeck, overviewMessage } from "./deck";

const DECK = `<!DOCTYPE html>\n<html lang="en">\n<head>\n<title>Deck</title>\n</head>\n<body><script src="/portal/vendor/reveal/reveal.js"></script></body></html>`;

describe("isSlideDeck", () => {
  it("is true only for a document that loads the served runtime", () => {
    expect(isSlideDeck(DECK)).toBe(true);
    expect(isSlideDeck("<html><body><h1>Dashboard</h1></body></html>")).toBe(false);
    expect(isSlideDeck('<script src="https://cdn.example.com/reveal.js"></script>')).toBe(false);
  });
});

describe("overviewMessage", () => {
  it("is the runtime's postMessage form for toggleOverview", () => {
    expect(JSON.parse(overviewMessage())).toEqual({ method: "toggleOverview", args: [] });
  });
});
