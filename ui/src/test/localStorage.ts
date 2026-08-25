/**
 * An in-memory Storage, for the tests of code that persists a reader's own
 * preferences.
 *
 * The jsdom realm these tests run in has no localStorage, so every read and
 * write of it in the app is guarded and silently does nothing here. That guard
 * is correct — a browser with site data blocked behaves the same way — but it
 * means the persistence cannot be proved without supplying somewhere to persist
 * to. Stub this in with `vi.stubGlobal("localStorage", fakeLocalStorage())`.
 */
export function fakeLocalStorage(): Storage {
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
