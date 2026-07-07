// MIN_SEARCH_LEN is the shortest free-text query that issues a server search.
// Single-character full-text queries are wasteful and return noise, so search
// surfaces wait for at least this many characters (debounced) before querying.
export const MIN_SEARCH_LEN = 2;
