import { createContext } from "react";
import type { LucideIcon } from "lucide-react";

// A document's own controls (an HTML asset's Present, Overview and Export PDF)
// handed to the page as data rather than as buttons portaled into its control
// row (#2601). On a phone the asset page puts them in its More menu; a page
// that provides no registry gets the buttons as before.

/** DocAction is one of a document's own controls. */
export interface DocAction {
  id: string;
  label: string;
  icon: LucideIcon;
  onSelect: () => void;
  disabled?: boolean;
}

/**
 * DocActionsContext is where a renderer registers its controls, when the page
 * takes them; null renders them as buttons.
 */
export const DocActionsContext = createContext<((actions: DocAction[]) => void) | null>(null);
