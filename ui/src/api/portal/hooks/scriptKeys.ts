// scriptsKey roots every portal script query, so one invalidation refreshes
// the listing, any open detail, and the state beside it together. It lives in
// its own module because the script hooks are split across files that all
// invalidate the same root.
export const scriptsKey = ["portal", "scripts"] as const;
