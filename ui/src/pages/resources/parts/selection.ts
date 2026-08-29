import { useCallback, useMemo, useState } from "react";

/**
 * The files a person has picked out of a library, and what can be done to them.
 *
 * Re-filing forty resources used to mean opening forty Edit dialogs (#1530).
 * The selection is what makes one action cover all of them; it is held by the
 * page rather than by the table so it survives switching between the row list
 * and the image grid.
 */
export interface Selection {
  ids: string[];
  has: (id: string) => boolean;
  toggle: (id: string) => void;
  add: (ids: string[]) => void;
  clear: () => void;
}

/**
 * useSelection holds the picked ids.
 *
 * Nothing prunes the set when the view changes. A selection made in one folder
 * and carried into another is what makes "pick these, then go somewhere and act
 * on them" work at all, and the action bar states the count, so a selection the
 * person has forgotten is visible rather than silent.
 */
export function useSelection(): Selection {
  const [ids, setIds] = useState<string[]>([]);

  const toggle = useCallback((id: string) => {
    setIds((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));
  }, []);
  const add = useCallback((more: string[]) => {
    setIds((prev) => [...new Set([...prev, ...more])]);
  }, []);
  const clear = useCallback(() => setIds([]), []);

  return useMemo(
    () => ({ ids, has: (id: string) => ids.includes(id), toggle, add, clear }),
    [ids, toggle, add, clear],
  );
}
