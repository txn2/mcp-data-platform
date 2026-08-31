import { useId, useState } from "react";
import { ChevronDown } from "lucide-react";

import { Button } from "@/components/ui/button";
import { portalNavItems } from "@/components/layout/sidebar/navItems";
import { sectionIntroPath, sectionIntroFor, type SectionIntroCopy } from "./sectionIntros";

/**
 * SectionIntro states what a portal section is for.
 *
 * Open any section of the portal and its controls say what you can do but
 * nothing says what the section holds, and the difference between an asset and
 * a resource is the whole content model. The compact row is the at-a-glance
 * summary a returning reader sees; what belongs here, what does not, and where
 * that goes instead are progressively disclosed. The choice persists per
 * section, so a first-timer is taught once and a returning reader is not
 * nagged (#1570).
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

/** Absent means never opened, which reads as expanded: the reader has not been told yet. */
function readExpanded(key: string): boolean {
  try {
    return localStorage.getItem(key) !== "0";
  } catch {
    return true;
  }
}

function IntroCard({ intro }: { intro: SectionIntroCopy }) {
  const [expanded, setExpanded] = useState(() => readExpanded(intro.storageKey));
  const detailId = useId();
  const nav = portalNavItems.find((item) => item.path === intro.path);
  const Icon = nav?.icon;

  const toggle = () =>
    setExpanded((open) => {
      const next = !open;
      try {
        localStorage.setItem(intro.storageKey, next ? "1" : "0");
      } catch {
        /* private mode: just don't persist */
      }
      return next;
    });

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
          <div className="space-y-3 border-t px-4 py-3">
            {intro.detail}
            {/* Labelled lines rather than a paragraph: the reader is deciding
                which section a thing belongs in, and that is a comparison. */}
            <dl className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1">
                <dt className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                  What belongs here
                </dt>
                <dd className="text-sm leading-relaxed text-foreground">{intro.belongs}</dd>
              </div>
              <div className="space-y-1">
                <dt className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                  What does not
                </dt>
                <dd className="text-sm leading-relaxed text-muted-foreground">{intro.notHere}</dd>
              </div>
            </dl>
          </div>
        </div>
      </div>
    </section>
  );
}
