import { describe, expect, it } from "vitest";
import { formQuery, passQueryToForms } from "./formFrames";

describe("formFrames", () => {
  it("keeps the page's query and drops the parameters a form page keeps for itself", () => {
    expect(formQuery("?t=abc&form_viewer=forged&x=1", null)).toBe("t=abc&x=1");
    expect(formQuery("?t=abc&form_theme=light", "dark")).toBe("t=abc&form_theme=dark");
    expect(formQuery("", null)).toBe("");
  });

  it("adds the query to every form page address in markup, after a viewer token", () => {
    const html =
      '<iframe src="https://p.example.com/portal/forms/contact"></iframe>' +
      '<iframe src="https://p.example.com/portal/forms/survey_2?form_viewer=tok"></iframe>' +
      '<script src="/portal/forms/_/form.js"></script>';
    const out = passQueryToForms(html, "t=abc&x=1", true);
    expect(out).toContain('src="https://p.example.com/portal/forms/contact?t=abc&amp;x=1"');
    expect(out).toContain('src="https://p.example.com/portal/forms/survey_2?form_viewer=tok&amp;t=abc&amp;x=1"');
    expect(out).toContain('src="/portal/forms/_/form.js"');
  });

  it("writes & as itself in JSX source", () => {
    const jsx = 'const f = <iframe src="/portal/forms/contact?form_viewer=tok" />;';
    expect(passQueryToForms(jsx, "t=a&b=c", false)).toBe(
      'const f = <iframe src="/portal/forms/contact?form_viewer=tok&t=a&b=c" />;',
    );
  });

  it("leaves content alone with no query or no form", () => {
    const html = '<iframe src="/portal/forms/contact"></iframe>';
    expect(passQueryToForms(html, "", true)).toBe(html);
    expect(passQueryToForms("<p>none</p>", "t=1", true)).toBe("<p>none</p>");
  });

  it("points each form page at the origin the reader browses, which the asset frames allow", () => {
    const html = '<iframe src="http://localhost:8080/portal/forms/contact?form_viewer=tok"></iframe>';
    expect(passQueryToForms(html, "", true, "http://localhost:28082")).toBe(
      '<iframe src="http://localhost:28082/portal/forms/contact?form_viewer=tok"></iframe>',
    );
    expect(passQueryToForms('<iframe src="/portal/forms/a"></iframe>', "t=1", true, "https://p.example.com")).toBe(
      '<iframe src="https://p.example.com/portal/forms/a?t=1"></iframe>',
    );
  });
});
