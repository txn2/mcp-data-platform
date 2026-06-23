import { useEffect, useState } from "react";

// useDebounced returns value after it has stopped changing for delayMs, so a
// search box issues one request after the user pauses rather than one per
// keystroke. Shared across the knowledge search and list views.
export function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}
