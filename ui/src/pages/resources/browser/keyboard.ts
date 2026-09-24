import type { KeyboardEvent } from "react";
import type { FmSelection } from "./useFmSelection";

/** What the listing's keys act on. */
interface ListingKeys {
  order: string[];
  selection: FmSelection;
  /** True while a name field has the keyboard, which keeps its own keys. */
  editing: boolean;
  onOpen: (key: string) => void;
  onUp: () => void;
  onRename: () => void;
  onDelete: () => void;
}

/** Up/Down move the focus, Shift extending the selection. */
function step(e: KeyboardEvent, k: ListingKeys) {
  e.preventDefault();
  const i = k.selection.focus === null ? -1 : k.order.indexOf(k.selection.focus);
  const delta = e.key === "ArrowDown" ? 1 : -1;
  const j = i < 0 ? 0 : Math.max(0, Math.min(k.order.length - 1, i + delta));
  const key = k.order[j];
  if (key !== undefined) k.selection.step(key, e.shiftKey);
}

type Handler = (e: KeyboardEvent, k: ListingKeys, mod: boolean) => boolean;

/** Each key the listing answers, returning true when it handled the press. */
const KEYS: Record<string, Handler> = {
  ArrowDown: (e, k) => (step(e, k), true),
  ArrowUp: (e, k) => (step(e, k), true),
  Enter: (_e, k) => (k.selection.focus !== null && k.onOpen(k.selection.focus), true),
  Delete: (_e, k) => (k.onDelete(), true),
  Backspace: (_e, k, mod) => (mod ? k.onDelete() : k.onUp(), true),
  F2: (_e, k) => (k.onRename(), true),
  Escape: (_e, k) => (k.selection.clear(), true),
  a: (e, k, mod) => {
    if (!mod) return false;
    e.preventDefault();
    k.selection.add(k.order);
    return true;
  },
};

/**
 * listingKey is the listing's keyboard (#1872): Up/Down move, Shift extends,
 * Enter opens, Backspace goes up, F2 renames, Cmd/Ctrl-Backspace or Delete
 * deletes, Cmd/Ctrl-A selects everything, Escape clears.
 */
export function listingKey(k: ListingKeys) {
  return (e: KeyboardEvent) => {
    if (k.editing) return;
    const handler = KEYS[e.key.length === 1 ? e.key.toLowerCase() : e.key];
    if (!handler) return;
    if (k.order.length === 0 && e.key !== "Backspace") return;
    handler(e, k, e.metaKey || e.ctrlKey);
  };
}
