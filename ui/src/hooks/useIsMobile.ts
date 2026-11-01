import { useEffect, useState } from "react";

/** The width below which the portal lays itself out for a phone: no sidebar
 * rail, a one-row page header, and the Expand view for content (#2390). */
export const MOBILE_BREAKPOINT = 768;

/** Whether the viewport is narrower than MOBILE_BREAKPOINT, following it as
 * the window is resized or the phone is turned. */
export function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(
    () => typeof window !== "undefined" && window.innerWidth < MOBILE_BREAKPOINT,
  );
  useEffect(() => {
    // An environment with no media queries (a test DOM) keeps the width read
    // at mount.
    if (typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`);
    const handler = (e: MediaQueryListEvent) => setIsMobile(e.matches);
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);
  return isMobile;
}
