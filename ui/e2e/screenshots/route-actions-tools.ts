import { type Page } from "@playwright/test";

/**
 * runToolTryIt fills the Try It form and executes the tool, so the capture
 * shows what the tab is for.
 *
 * Opened cold, the tab is an empty form over "History (0) - No test calls
 * yet", which documents neither the result rendering nor the execution
 * history the docs describe beside it. The admin tool runner is mocked
 * (`POST /tools/call`), so running it here is a real round trip through the
 * same path the page uses.
 */
export async function runToolTryIt(page: Page): Promise<void> {
  const sql = page.getByPlaceholder("SELECT ...");
  await sql.waitFor({ timeout: 5_000 });
  await sql.fill(
    "SELECT r.name AS region, SUM(ti.line_total) AS revenue\n" +
      "FROM warehouse.public.transactions AS t\n" +
      "INNER JOIN warehouse.public.transaction_items AS ti\n" +
      "        ON ti.transaction_id = t.transaction_id\n" +
      "INNER JOIN warehouse.public.stores AS s ON s.store_id = t.store_id\n" +
      "INNER JOIN warehouse.public.regions AS r ON r.region_id = s.region_id\n" +
      "GROUP BY r.name\n" +
      "ORDER BY revenue DESC",
  );
  await page.getByRole("button", { name: "Execute" }).click();
  // The mock delays up to 800ms before answering; wait for the history to
  // record the call rather than for a fixed interval.
  await page
    .getByText(/History \(1\)/)
    .waitFor({ timeout: 10_000 })
    .catch(() => {});
  await page.waitForTimeout(600);
}

/**
 * waitForCollectionThumbnails holds until every collection card has rendered a
 * thumbnail rather than its folder fallback.
 *
 * A collection's thumbnail is generated in the browser after the list paints,
 * so a capture taken on the default settle timeout publishes a grid where some
 * cards are mosaics and others are placeholder icons -- which reads as missing
 * data rather than as work still in flight.
 */
export async function waitForCollectionThumbnails(page: Page): Promise<void> {
  await page
    .waitForFunction(
      () => {
        const cards = document.querySelectorAll('[data-slot="card"]');
        if (!cards.length) return false;
        return [...cards].every((c) => c.querySelector("img") !== null);
      },
      null,
      { timeout: 45_000 },
    )
    .catch(() => {});
  await page.waitForTimeout(800);
}
