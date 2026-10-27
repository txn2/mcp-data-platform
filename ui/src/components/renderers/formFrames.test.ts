import { describe, expect, it } from "vitest";
import { formQuery, passQueryToForms } from "./formFrames";

describe("formFrames", () => {
  it("keeps the page's query and drops the parameters a form page keeps for itself", () => {
    expect(formQuery("?t=abc&form_viewer=forged&form_embed=forged&x=1", null)).toBe("t=abc&x=1");
    expect(formQuery("?t=abc&form_theme=light", "dark")).toBe("t=abc&form_theme=dark");
    expect(formQuery("", null)).toBe("");
  });

  it("adds the query to every marked form page address in markup, after what the platform wrote", () => {
    const html =
      '<iframe src="https://p.example.com/forms/contact?form_embed=m1"></iframe>' +
      '<iframe src="https://p.example.com/forms/survey_2?form_embed=m2&form_viewer=tok"></iframe>' +
      '<script src="/forms/_/form.js"></script>';
    const out = passQueryToForms(html, "t=abc&x=1", true);
    expect(out).toContain('src="https://p.example.com/forms/contact?form_embed=m1&amp;t=abc&amp;x=1"');
    expect(out).toContain('src="https://p.example.com/forms/survey_2?form_embed=m2&form_viewer=tok&amp;t=abc&amp;x=1"');
    expect(out).toContain('src="/forms/_/form.js"');
  });

  it("writes & as itself in JSX source", () => {
    const jsx = 'const f = <iframe src="/forms/contact?form_embed=m&form_viewer=tok" />;';
    expect(passQueryToForms(jsx, "t=a&b=c", false)).toBe(
      'const f = <iframe src="/forms/contact?form_embed=m&form_viewer=tok&t=a&b=c" />;',
    );
  });

  it("leaves content alone with no query or no form", () => {
    const html = '<iframe src="/forms/contact?form_embed=m"></iframe>';
    expect(passQueryToForms(html, "", true)).toBe(html);
    expect(passQueryToForms("<p>none</p>", "t=1", true)).toBe("<p>none</p>");
  });

  it("leaves another site's /forms/ address as written (#2331)", () => {
    const html = '<a href="https://www.example.org/forms/signup?ref=1">Sign up</a><iframe src="/forms/plain"></iframe>';
    expect(passQueryToForms(html, "t=1", true, "https://p.example.com")).toBe(html);
  });

  it("points each form page at the origin the reader browses, which the asset frames allow", () => {
    const html = '<iframe src="http://localhost:8080/forms/contact?form_embed=m&amp;form_viewer=tok"></iframe>';
    expect(passQueryToForms(html, "", true, "http://localhost:28082")).toBe(
      '<iframe src="http://localhost:28082/forms/contact?form_embed=m&amp;form_viewer=tok"></iframe>',
    );
    expect(passQueryToForms('<iframe src="/forms/a?form_embed=m"></iframe>', "t=1", true, "https://p.example.com")).toBe(
      '<iframe src="https://p.example.com/forms/a?form_embed=m&amp;t=1"></iframe>',
    );
  });
});
