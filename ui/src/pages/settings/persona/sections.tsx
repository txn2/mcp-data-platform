import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

// The persona editor is one page of sections, grouped by the surface each
// governs (#2257). The index on the left jumps to a section; nothing on the
// page applies to a section it does not sit in.

export type SectionID =
  | "identity"
  | "summary"
  | "tools"
  | "connections"
  | "api"
  | "instructions"
  | "portal";

export interface SectionDef {
  id: SectionID;
  label: string;
}

export const SECTION_GROUPS: { label: string | null; sections: SectionDef[] }[] = [
  {
    label: null,
    sections: [
      { id: "identity", label: "Identity" },
      { id: "summary", label: "Access summary" },
    ],
  },
  {
    label: "AI assistant (MCP)",
    sections: [
      { id: "tools", label: "Tools" },
      { id: "connections", label: "Connections" },
      { id: "api", label: "API endpoints" },
      { id: "instructions", label: "Instructions" },
    ],
  },
  {
    label: "Portal (browser)",
    sections: [{ id: "portal", label: "Portal access" }],
  },
];

export function sectionAnchor(id: SectionID): string {
  return `persona-section-${id}`;
}

/** SectionIndex lists the sections by group and jumps to the one chosen. */
export function SectionIndex({ active }: { active: SectionID | null }) {
  return (
    <nav aria-label="Persona sections" className="space-y-4 px-4 py-4 text-sm">
      {SECTION_GROUPS.map((group, i) => (
        <div key={group.label ?? i}>
          {group.label && (
            <p className="mb-1 px-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
              {group.label}
            </p>
          )}
          <ul className="space-y-0.5">
            {group.sections.map((s) => (
              <li key={s.id}>
                <a
                  href={`#${sectionAnchor(s.id)}`}
                  onClick={(e) => {
                    e.preventDefault();
                    document.getElementById(sectionAnchor(s.id))?.scrollIntoView({ behavior: "smooth", block: "start" });
                  }}
                  className={cn(
                    "block rounded px-2 py-1 hover:bg-muted",
                    active === s.id && "bg-muted font-medium",
                  )}
                >
                  {s.label}
                </a>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}

/** EditorSection is one section of the page, with what it governs under its title. */
export function EditorSection({
  id,
  title,
  governs,
  children,
}: {
  id: SectionID;
  title: string;
  governs?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section id={sectionAnchor(id)} aria-labelledby={`${sectionAnchor(id)}-title`} className="scroll-mt-4 border-b px-6 py-6 last:border-b-0">
      <h3 id={`${sectionAnchor(id)}-title`} className="text-base font-semibold">
        {title}
      </h3>
      {governs && <p className="mt-1 max-w-3xl text-xs text-muted-foreground">{governs}</p>}
      <div className="mt-4">{children}</div>
    </section>
  );
}
