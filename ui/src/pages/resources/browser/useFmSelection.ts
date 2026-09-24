import { useCallback, useMemo, useState } from "react";
import { pickRange } from "./model";

/** The modifier keys a click carries, which decide what it does to the selection. */
export interface ClickMods {
  shiftKey: boolean;
  metaKey: boolean;
  ctrlKey: boolean;
}

/**
 * The rows a person has picked, where the keyboard focus is, and the anchor a
 * Shift-click ranges from: the selection model every file manager shares
 * (#1872).
 */
export interface FmSelection {
  keys: string[];
  has: (key: string) => boolean;
  /** The row the keyboard is on. */
  focus: string | null;
  /** Click, Shift-click (range from the anchor), Cmd/Ctrl-click (toggle). */
  click: (key: string, mods: ClickMods, order: string[]) => void;
  /** The checkbox: toggles one row and makes it the anchor. */
  toggle: (key: string) => void;
  /** Up/Down: moves the focus, selecting the row alone or extending to it. */
  step: (key: string, extend: boolean) => void;
  /** Select exactly these, with the first as anchor and focus. */
  only: (keys: string[]) => void;
  /** Add these to the selection (Cmd/Ctrl-A, the header checkbox). */
  add: (keys: string[]) => void;
  clear: () => void;
}

export function useFmSelection(): FmSelection {
  const [keys, setKeys] = useState<string[]>([]);
  const [anchor, setAnchor] = useState<string | null>(null);
  const [focus, setFocus] = useState<string | null>(null);

  const click = useCallback(
    (key: string, mods: ClickMods, order: string[]) => {
      setFocus(key);
      if (mods.shiftKey) {
        setKeys(pickRange(order, anchor, key));
        return;
      }
      setAnchor(key);
      if (mods.metaKey || mods.ctrlKey) {
        setKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));
        return;
      }
      setKeys([key]);
    },
    [anchor],
  );

  const toggle = useCallback((key: string) => {
    setAnchor(key);
    setFocus(key);
    setKeys((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));
  }, []);

  const step = useCallback((key: string, extend: boolean) => {
    setFocus(key);
    if (extend) {
      setKeys((prev) => (prev.includes(key) ? prev : [...prev, key]));
      return;
    }
    setAnchor(key);
    setKeys([key]);
  }, []);

  const only = useCallback((next: string[]) => {
    setKeys(next);
    setAnchor(next[0] ?? null);
    setFocus(next[0] ?? null);
  }, []);

  const add = useCallback((more: string[]) => {
    setKeys((prev) => [...new Set([...prev, ...more])]);
  }, []);

  const clear = useCallback(() => {
    setKeys([]);
    setAnchor(null);
  }, []);

  return useMemo(
    () => ({
      keys,
      has: (k: string) => keys.includes(k),
      focus,
      click,
      toggle,
      step,
      only,
      add,
      clear,
    }),
    [keys, focus, click, toggle, step, only, add, clear],
  );
}
