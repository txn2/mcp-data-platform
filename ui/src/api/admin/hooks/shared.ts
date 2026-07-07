// Shared local constants used across admin hook modules.

// Refresh interval for auto-updating queries (30 seconds)
export const REFETCH_INTERVAL = 30_000;

/** Maximum asset size for auto-loading content in the admin viewer (matches portal threshold). */
export const ADMIN_LARGE_ASSET_THRESHOLD = 2 * 1024 * 1024; // 2 MB
