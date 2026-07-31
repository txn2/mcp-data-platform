import type { ReactNode } from "react";
import { ModalScroll } from "@/components/ModalShell";

/**
 * Overlay is the shared backdrop for the resource modals. Each of them brings
 * its own panel chrome, so this delegates to ModalScroll: the panel keeps its
 * natural height and the backdrop scrolls.
 *
 * It previously centered with `items-center` over a scrolling overlay, which
 * put the top of a tall panel -- its title and close button -- above the
 * scroll container's start, where nothing could reach it. ModalScroll centers
 * while the content fits and pins to the top once it does not.
 */
export function Overlay({
  children,
  onClose,
}: {
  children: ReactNode;
  onClose: () => void;
}) {
  return <ModalScroll onClose={onClose}>{children}</ModalScroll>;
}
