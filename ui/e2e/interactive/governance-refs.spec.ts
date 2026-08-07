import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for knowledge pages linking to governance entities
// (#1159): the deep link a stored glossary-term, tag, or domain reference
// produces, arriving in the assembled app. These assert the whole chain --
// AppShell's route matching, the hash that selects the Catalog inner tab, and
// the tab reading the entity out of `?urn=` -- rather than the tab alone.

const tagFilter = /Filter tags by name/;
const domainFilter = /Filter domains by name/;

// deepLink is what `catalogHref` builds for a stored reference.
function deepLink(urn: string, sub: string): string {
  return `/portal/knowledge/catalog?urn=${encodeURIComponent(urn)}#${sub}`;
}

test.describe("Governance reference deep links", () => {
  test("a glossary-term reference opens the term on the Glossary tab", async ({ page }) => {
    await authenticate(page);
    await page.goto(deepLink("urn:li:glossaryTerm:Revenue", "glossary"));

    // The term opens directly, without the reader walking the tree to it: the
    // citation carries only the URN, which the by-URN term read resolves.
    await expect(page.getByRole("heading", { name: /Revenue/ })).toBeVisible();
    await expect(page.getByText("urn:li:glossaryTerm:Revenue")).toBeVisible();
    await expect(page.getByText("Tables annotated with this term")).toBeVisible();
  });

  test("a tag reference opens the tag on the Tags tab", async ({ page }) => {
    await authenticate(page);
    await page.goto(deepLink("urn:li:tag:pii", "tags"));

    await expect(page.getByRole("heading", { name: /pii/ })).toBeVisible();
    await expect(page.getByText("Contains personally identifiable information.")).toBeVisible();
    await expect(page.getByText("Tables carrying this tag")).toBeVisible();
  });

  test("a domain reference opens the domain on the Domains tab", async ({ page }) => {
    await authenticate(page);
    await page.goto(deepLink("urn:li:domain:finance", "domains"));

    await expect(page.getByRole("heading", { name: /Finance/ })).toBeVisible();
    await expect(page.getByText("Revenue, billing, and reporting.")).toBeVisible();
  });

  test("going back drops the deep link, so a reload lands on the list", async ({ page }) => {
    await authenticate(page);
    await page.goto(deepLink("urn:li:tag:pii", "tags"));
    await expect(page.getByRole("heading", { name: /pii/ })).toBeVisible();

    await page.getByRole("button", { name: /Back to tags/ }).click();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
    expect(page.url()).not.toContain("urn=");

    await page.reload();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
  });

  test("a reference to an entity this connection does not list says so", async ({ page }) => {
    // A tag and a domain have no by-URN read upstream, so a URN the vocabulary
    // list does not hold cannot be opened at all. It must be reported, not
    // rendered as an entry with a blank description.
    await authenticate(page);
    await page.goto(deepLink("urn:li:domain:elsewhere", "domains"));

    await expect(page.getByText(/lists no domain with the URN/)).toBeVisible();
    await page.getByRole("button", { name: /Back to domains/ }).click();
    await expect(page.getByPlaceholder(domainFilter)).toBeVisible();
  });

  test("an author attaches a glossary term by name, and the chip links to it", async ({ page }) => {
    // The whole point of the picker: attaching "Net Sales" never means typing
    // the URN DataHub gave that term.
    await authenticate(page);
    await page.goto("/portal/knowledge/pages/kp-seed-2");
    await expect(page.getByRole("heading", { name: "Revenue Definition" }).first()).toBeVisible();

    // The picker is a SectionCard, so its box is the innermost card carrying the
    // heading. The type facet is a Radix listbox (not a native select), and its
    // options are portalled out of the card, so they are chosen off `page`.
    const picker = page.locator("[data-slot='card']").filter({ hasText: "Manual references" }).last();
    await picker.getByLabel("Reference type").click();
    await page.getByRole("option", { name: "Glossary term" }).click();
    await picker.getByPlaceholder(/Search glossary terms to reference/).fill("net");
    await picker.getByRole("button", { name: /Net Sales/ }).click();

    // The attached reference comes back named, not as the URN that was stored.
    await expect(picker.getByRole("link", { name: "Net Sales" })).toBeVisible();

    // And it links to the Glossary tab, where the term is managed.
    await picker.getByRole("link", { name: "Net Sales" }).click();
    await expect(page.getByRole("heading", { name: /Net Sales/ })).toBeVisible();
    expect(page.url()).toContain("#glossary");
  });

  test("a governance detail lists the knowledge pages that reference it", async ({ page }) => {
    // The reverse of the link: a steward reading a term sees what has been
    // written about it, and can open that page from here.
    await authenticate(page);
    await page.goto(deepLink("urn:li:tag:pii", "tags"));

    await expect(page.getByText(/knowledge page[s]? reference[s]? this/)).toBeVisible();
    await page.getByRole("link", { name: "Customer PII Handling" }).click();
    await expect(page.getByRole("heading", { name: "Customer PII Handling" }).first()).toBeVisible();
  });

  test("a URN meant for another tab opens that tab's list, not a failing read", async ({ page }) => {
    // Each inner tab claims only its own URN kinds, so a stale or hand-edited
    // link degrades to the list rather than to an error.
    await authenticate(page);
    await page.goto(deepLink("urn:li:glossaryTerm:Revenue", "tags"));

    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
    await expect(page.getByRole("heading", { name: /^Revenue$/ })).toHaveCount(0);
  });
});
