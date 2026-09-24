/**
 * runPool works through items with at most `concurrency` in flight at once, in
 * order, and resolves when every item has been attempted.
 *
 * A rejected item never stops the others: in a bulk upload the files already
 * stored stay stored and the rest keep going, and the caller records each
 * item's outcome inside `work`. `shouldStop` is asked before each item starts,
 * which is how a cancel lets the requests in flight finish and starts no more.
 */
export async function runPool<T>(
  items: readonly T[],
  concurrency: number,
  work: (item: T) => Promise<unknown>,
  shouldStop: () => boolean = () => false,
): Promise<void> {
  let next = 0;
  const lane = async (): Promise<void> => {
    while (next < items.length && !shouldStop()) {
      const item = items[next++] as T;
      try {
        await work(item);
      } catch {
        // The item's own outcome is the caller's to record; the pool moves on.
      }
    }
  };
  const lanes = Math.max(1, Math.min(concurrency, items.length));
  await Promise.all(Array.from({ length: lanes }, lane));
}
