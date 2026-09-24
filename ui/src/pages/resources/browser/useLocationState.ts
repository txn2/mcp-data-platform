import { useCallback, useEffect, useRef, useState } from "react";
import type { ResourceSort } from "@/api/resources/types";
import { folderPath, libraryPath, readLibraryView } from "../parts/libraryUrl";
import { DEFAULT_ROOT, rootFor, type ResourceRoot } from "../scopes";

interface Section {
  /** "/resources" or "/admin/resources". */
  basePath: string;
  /** The in-app location, query string included. */
  location: string;
  onNavigate?: (path: string, opts?: { replace?: boolean }) => void;
}

/**
 * useLocationState is where the page is standing, read off the address on
 * every render (#1530): which top-level folder, which folder in it, and the
 * search, tag and order narrowing the view. Moving is a navigation, so a
 * reload, Back and a pasted link all arrive at the same place.
 *
 * It also keeps the page's own Back and Forward (#1872): the places this page
 * has been, which the path bar's buttons step through.
 */
export function useLocationState(section: Section, roots: ResourceRoot[], emailOf: (id: string) => string) {
  const { basePath, location, onNavigate } = section;
  const view = readLibraryView(location, basePath, DEFAULT_ROOT);
  const root = rootFor(view.tab, roots, emailOf);

  const [searchInput, setSearchInput] = useState(view.q);
  const settled = useDebounced(searchInput);
  const [history, setHistory] = useState<{ back: string[]; forward: string[] }>({ back: [], forward: [] });
  const here = folderPath(basePath, view, DEFAULT_ROOT, view.path);

  // A settled query different from the address writes the address; an address
  // different from the box (Back, a link, a folder opened while typing)
  // refills the box. Neither drives the other into a loop.
  useEffect(() => {
    if (settled === view.q) return;
    onNavigate?.(libraryPath(basePath, { ...view, q: settled }, DEFAULT_ROOT), { replace: true });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settled]);
  useEffect(() => {
    if (view.q !== settled) setSearchInput(view.q);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [view.q]);

  const go = useCallback(
    (tab: string, path: string) => {
      const next = folderPath(basePath, { ...view, tab }, DEFAULT_ROOT, path);
      if (next === here && view.q === "" && view.tag === "") return;
      setHistory((h) => ({ back: [...h.back, here], forward: [] }));
      setSearchInput("");
      onNavigate?.(next);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [basePath, location, onNavigate, here],
  );

  const back = () => {
    const to = history.back[history.back.length - 1];
    if (to === undefined) return;
    setHistory({ back: history.back.slice(0, -1), forward: [...history.forward, here] });
    onNavigate?.(to);
  };

  const forward = () => {
    const to = history.forward[history.forward.length - 1];
    if (to === undefined) return;
    setHistory({ back: [...history.back, here], forward: history.forward.slice(0, -1) });
    onNavigate?.(to);
  };

  const replace = (next: Partial<typeof view>) =>
    onNavigate?.(libraryPath(basePath, { ...view, ...next }, DEFAULT_ROOT), { replace: true });

  return {
    root,
    path: view.path,
    /** The debounced search, which is what the listing asks for. */
    query: view.q,
    searchInput,
    setSearchInput,
    tag: view.tag,
    setTag: (tag: string) => replace({ tag, q: "" }),
    sort: view.sort,
    setSort: (sort: ResourceSort) => replace({ sort }),
    go,
    back,
    forward,
    canBack: history.back.length > 0,
    canForward: history.forward.length > 0,
    /** The address of the place in view, for the entry a viewer returns to. */
    address: libraryPath(basePath, { ...view, q: searchInput }, DEFAULT_ROOT),
  };
}

/** useDebounced settles a value that changes per keystroke. */
function useDebounced(value: string, delay = 300): string {
  const [settled, setSettled] = useState(value);
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const timer = setTimeout(() => setSettled(value), delay);
    return () => clearTimeout(timer);
  }, [value, delay]);
  return settled;
}
