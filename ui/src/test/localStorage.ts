/**
 * An in-memory Storage, installed as localStorage and sessionStorage before
 * every test by setup.ts (#1976).
 *
 * The runtime cannot be left to provide one. Under Node 26, Node's own
 * experimental localStorage global shadows jsdom's and is unusable without
 * --localstorage-file, so a write silently did nothing on a laptop while CI's
 * Node 22 kept it: #1975 went red in CI when one Flow test's saved view opened
 * the next three on it. Each test now starts on an empty store under any
 * runtime, and a test that means the store to be absent stubs it away with
 * `vi.stubGlobal("localStorage", undefined)`.
 */
export function memoryStorage(): Storage {
  const store = new Map<string, string>();
  return {
    get length() {
      return store.size;
    },
    key: (i: number) => [...store.keys()][i] ?? null,
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, String(v)),
    removeItem: (k: string) => void store.delete(k),
    clear: () => store.clear(),
  };
}

/** Replaces both storages on the global scope and on window with empty ones. */
export function installMemoryStorage(): void {
  for (const scope of new Set<object>([globalThis, typeof window === "undefined" ? globalThis : window])) {
    for (const name of ["localStorage", "sessionStorage"]) {
      Object.defineProperty(scope, name, { value: memoryStorage(), configurable: true, writable: true });
    }
  }
}
