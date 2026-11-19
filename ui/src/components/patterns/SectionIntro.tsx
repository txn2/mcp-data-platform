import { useId, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useIsMobile } from "@/hooks/useIsMobile";
import { portalNavItems } from "@/components/layout/sidebar/navItems";
import { sectionIntroPath, sectionIntroFor, type SectionIntroCopy } from "./sectionIntros";

/**
 * SectionIntro states what a portal section is for.
 *
 * Open any section of the portal and its controls say what you can do but
 * nothing says what the section holds, and the difference between an asset and
 * a resource is the whole content model. The compact row is the one-line
 * description a returning reader sees; the fuller one is progressively
 * disclosed, and the choice persists per section, so a first-timer reads it
 * once and a returning reader is not nagged (#1570).
 *
 * A section whose summary is better drawn than written supplies its own
 * compact row (Knowledge draws the Memory -> Insight -> Knowledge pipeline);
 * everything else gets the section's own nav icon and one sentence. The name is
 * not repeated: the header bar above and the lit nav item beside it carry it.
 */
export function SectionIntro({ route }: { route: string }) {
  const path = sectionIntroPath(route);
  const intro = path ? sectionIntroFor(path) : undefined;
  if (!intro) return null;
  // Keyed by section: the shell keeps this element in the same place across a
  // client-side move between sections, so without a key React would reuse the
  // card and the section moved to would inherit the open/closed state of the
  // one left behind, having never read its own.
  return <IntroCard key={intro.path} intro={intro} />;
}

/**
 * Absent means never opened, which reads as expanded on a wide screen (the
 * reader has not been told yet) and collapsed on a phone, where the card would
 * take a fifth of the screen on every visit (#2723). An open or close on
 * either is remembered for both.
 */
function readExpanded(key: string, fallback: boolean): boolean {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : v !== "0";
  } catch {
    return fallback;
  }
}

/** useExpanded is a section's open or closed choice, remembered per section. */
function useExpanded(key: string, fallback: boolean): [boolean, () => void] {
  const [expanded, setExpanded] = useState(() => readExpanded(key, fallback));
  const toggle = () =>
    setExpanded((open) => {
      const next = !open;
      try {
        localStorage.setItem(key, next ? "1" : "0");
      } catch {
        /* private mode: just don't persist */
      }
      return next;
    });
  return [expanded, toggle];
}

function IntroCard({ intro }: { intro: SectionIntroCopy }) {
  const isMobile = useIsMobile();
  const [expanded, toggle] = useExpanded(intro.storageKey, !isMobile);
  if (isMobile && !expanded) {
    // On a phone the closed intro is one link (#2723).
    return (
      <div data-testid="section-intro" className="-mt-1 mb-2 flex justify-end">
        <Button variant="ghost" size="sm" onClick={toggle} aria-expanded={false} className="min-h-11 text-primary">
          About this page
          <ChevronRight />
        </Button>
      </div>
    );
  }
  return <IntroPanel intro={intro} expanded={expanded} toggle={toggle} />;
}

function IntroPanel({ intro, expanded, toggle }: { intro: SectionIntroCopy; expanded: boolean; toggle: () => void }) {
  const detailId = useId();
  const nav = portalNavItems.find((item) => item.path === intro.path);
  const Icon = nav?.icon;

  return (
    <section
      data-testid="section-intro"
      aria-label={nav ? `What ${nav.label} is for` : "What this section is for"}
      className="mb-4 overflow-hidden rounded-xl border bg-card"
    >
      <div className="flex items-center gap-3 px-4 py-3">
        {intro.compact ?? (
          <div className="flex min-w-0 flex-1 items-center gap-2.5">
            {Icon && (
              <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/10 ring-1 ring-primary/20">
                <Icon aria-hidden className="h-4 w-4 text-primary" />
              </span>
            )}
            {/* The section is named by the header bar directly above and by the
                lit nav item beside it, so the row carries the sentence rather
                than repeating the name a third time. */}
            <span className="min-w-0 text-sm leading-snug text-foreground">{intro.summary}</span>
          </div>
        )}

        <Button
          variant="ghost"
          size="xs"
          onClick={toggle}
          aria-expanded={expanded}
          aria-controls={detailId}
          aria-label={
            expanded ? "Hide what this section is for" : "Show what this section is for"
          }
          className="ml-auto shrink-0 text-muted-foreground"
        >
          <span className="hidden sm:inline">
            {expanded ? "Hide" : (intro.toggleLabel ?? "What is this?")}
          </span>
          <ChevronDown
            className={`transition-transform duration-200 ${expanded ? "rotate-180" : ""}`}
          />
        </Button>
      </div>

      {/* Grid-rows 0fr/1fr animates to content height with no max-height guess.
          The detail stays in the DOM when closed, so aria-hidden is what takes
          it out of the reading order a screen reader follows. */}
      <div
        id={detailId}
        data-testid="section-intro-detail"
        aria-hidden={!expanded}
        className={`grid transition-all duration-300 ease-out ${
          expanded ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0"
        }`}
      >
        <div className="overflow-hidden">
          <p className="border-t px-4 py-3 text-sm leading-relaxed text-muted-foreground">
            {intro.about}
          </p>
        </div>
      </div>
    </section>
  );
}
