import { useEffect, useState } from "react";
import type { TextQuoteAnchor } from "@/api/portal/types";

const CONTEXT_LEN = 32;
const MAX_QUOTE = 400;

// closestAnchorable walks up from a node to the nearest element marked
// data-feedback-anchorable (the markdown/plaintext content containers).
function closestAnchorable(node: Node | null): HTMLElement | null {
  let el: Node | null = node;
  while (el && el.nodeType !== Node.ELEMENT_NODE) el = el.parentNode;
  return (el as HTMLElement | null)?.closest?.("[data-feedback-anchorable]") ?? null;
}

// buildTextQuote turns the current selection into a W3C-style text-quote anchor
// (exact text plus a little surrounding context), or null if the selection is
// empty or lands outside an anchorable content container.
function buildTextQuote(): TextQuoteAnchor | null {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null;

  const exact = sel.toString().trim();
  if (!exact) return null;

  const container = closestAnchorable(sel.anchorNode);
  if (!container || !closestAnchorable(sel.focusNode)) return null;

  // Cap the quote first, then derive prefix/suffix relative to the *capped*
  // quote so exact/prefix/suffix stay internally consistent even for long
  // selections. prefix/suffix come from the first occurrence of the quote; for
  // a phrase that repeats this may describe the wrong instance, but the exact
  // quote is still captured and re-resolution/highlighting is a later
  // enhancement, so first-match is acceptable here.
  const quote = exact.slice(0, MAX_QUOTE);
  const full = container.textContent ?? "";
  const idx = full.indexOf(quote);
  const anchor: TextQuoteAnchor = { type: "text_quote", exact: quote };
  if (idx >= 0) {
    const prefix = full.slice(Math.max(0, idx - CONTEXT_LEN), idx);
    const suffix = full.slice(idx + quote.length, idx + quote.length + CONTEXT_LEN);
    if (prefix) anchor.prefix = prefix;
    if (suffix) anchor.suffix = suffix;
  }
  return anchor;
}

// useTextQuoteAnchor tracks the most recent text selection made inside any
// [data-feedback-anchorable] element on the page, so the new-thread form can
// offer to pin feedback to it. Selections elsewhere (or cleared selections)
// leave the last anchorable selection available until a new one is made.
export function useTextQuoteAnchor(): {
  availableAnchor: TextQuoteAnchor | null;
  clear: () => void;
} {
  const [anchor, setAnchor] = useState<TextQuoteAnchor | null>(null);

  useEffect(() => {
    const onSelect = () => {
      const a = buildTextQuote();
      if (a) setAnchor(a);
    };
    document.addEventListener("selectionchange", onSelect);
    return () => document.removeEventListener("selectionchange", onSelect);
  }, []);

  return { availableAnchor: anchor, clear: () => setAnchor(null) };
}
