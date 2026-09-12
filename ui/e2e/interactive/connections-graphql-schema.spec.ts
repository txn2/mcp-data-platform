import { test, expect, type Locator, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// A graphql connection's Schema card, driven through the assembled portal.
//
// A read the endpoint refuses keeps the schema the connection holds and records
// the refusal beside it (#1676), so "holds a schema" and "the last read failed"
// are independent states. The card collapsed them: any error rendered as "The
// platform holds no schema for this connection", so an operator whose uploaded
// schema was live and serving discovery read that the upload was lost and
// uploaded it again (#1689).
//
// Every assertion is scoped to the card, because the connection's own
// description names its schema's source in prose and would otherwise satisfy a
// page-wide text match.

async function openSchemaCard(page: Page, name: string): Promise<Locator> {
  await authenticate(page);
  await page.goto(`/portal/admin/connections?kind=graphql&name=${name}`);
  await expect(page.getByRole("heading", { name, level: 2 })).toBeVisible();
  const card = page
    .locator('[data-slot="card"]')
    .filter({ has: page.getByRole("heading", { name: "Schema", exact: true }) });
  await expect(card).toBeVisible();
  return card;
}

test.describe("Schema card — a held schema whose re-read failed", () => {
  test("shows the schema it serves and the refusal beside it", async ({
    page,
  }) => {
    const card = await openSchemaCard(page, "acme-orders-graphql");

    // The schema the connection serves discovery from: how many operations,
    // where it came from, when it was read, which version.
    await expect(card.getByText("8 operations")).toBeVisible();
    await expect(card.getByText("uploaded", { exact: true })).toBeVisible();
    await expect(card.getByText(/^read \S/)).toBeVisible();
    await expect(card.getByText("ff68d87b41c2")).toBeVisible();

    // The refusal, as a failed refresh of a schema that is still serving.
    await expect(
      card.getByText(
        /last attempt to re-read this schema from the endpoint failed/i,
      ),
    ).toBeVisible();
    await expect(
      card.getByText(/HTTP 302 to the introspection query/),
    ).toBeVisible();
    await expect(
      card.getByText(/still serving the schema above/i),
    ).toBeVisible();

    // What an operator must not read on a connection that holds a schema.
    await expect(card.getByText(/holds no schema/i)).toHaveCount(0);
  });

  test("keeps saying so after a re-read the endpoint refuses", async ({
    page,
  }) => {
    const card = await openSchemaCard(page, "acme-orders-graphql");
    await card.getByRole("button", { name: "Re-read from endpoint" }).click();

    // The platform answers 502 and leaves the stored schema in place, so the
    // card reports the attempt without retracting the schema.
    await expect(card.getByText("8 operations")).toBeVisible();
    await expect(card.getByText("ff68d87b41c2")).toBeVisible();
    await expect(card.getByText(/holds no schema/i)).toHaveCount(0);
    // The refusal is already on the card as the connection's recorded state,
    // so the button's own report points at it rather than printing it twice.
    await expect(
      card.getByText("The read failed, for the reason already shown above."),
    ).toBeVisible();
    await expect(
      card.getByText(/HTTP 302 to the introspection query/),
    ).toHaveCount(1);
  });
});

test.describe("Schema card — no schema at all", () => {
  test("says the platform holds none and names the cause", async ({ page }) => {
    const card = await openSchemaCard(page, "acme-partners-graphql");

    await expect(
      card.getByText(/The platform holds no schema for this connection/),
    ).toBeVisible();
    await expect(card.getByText(/introspection is not allowed/i)).toBeVisible();
    // Nothing on the card claims a schema is being served.
    await expect(card.getByText(/operations/)).toHaveCount(0);
    await expect(card.getByText("uploaded", { exact: true })).toHaveCount(0);
  });
});
