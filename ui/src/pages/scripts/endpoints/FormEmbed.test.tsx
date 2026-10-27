import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { FormEmbed } from "./FormEmbed";
import { absolutePageURL, embedSnippet } from "./embedSnippet";

afterEach(cleanup);

// #2331: the snippet a site embeds a form's hosted page with.
describe("FormEmbed", () => {
  it("frames the hosted page and sizes the frame only from the platform's own messages", () => {
    const snippet = embedSnippet("https://data.example.com/forms/contact", "contact");
    expect(snippet).toContain('<iframe src="https://data.example.com/forms/contact" title="contact" data-platform-form');
    expect(snippet).toContain('e.origin !== "https://data.example.com"');
    expect(snippet).toContain('e.data.type !== "mcp-form:height"');
    expect(snippet).toContain("f.contentWindow === e.source");
  });

  it("reads a path the platform wrote against the browsed origin", () => {
    expect(absolutePageURL("/forms/contact", "https://p.example.com/")).toBe("https://p.example.com/forms/contact");
    expect(absolutePageURL("https://data.example.com/forms/contact", "https://p.example.com")).toBe(
      "https://data.example.com/forms/contact",
    );
  });

  it("offers the snippet and says which sites may show it", () => {
    render(<FormEmbed pageUrl="https://data.example.com/forms/contact" name="contact" allowedOrigins={[]} />);
    expect(screen.getByRole("button", { name: "Copy the embed snippet" })).toBeInTheDocument();
    expect(screen.getByLabelText("Embed snippet").textContent).toContain("/forms/contact");
    expect(screen.getByText(/only when Allowed origins lists the site's origin; it lists none\./)).toBeInTheDocument();
    cleanup();
    render(<FormEmbed pageUrl="https://data.example.com/forms/contact" name="contact" allowedOrigins={["https://www.example.org"]} />);
    expect(screen.queryByText(/it lists none/)).not.toBeInTheDocument();
  });
});
