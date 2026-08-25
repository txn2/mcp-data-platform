import { cn } from "@/lib/utils";

// An HTTP method reads as a color before it reads as a word, which is what
// makes a long operation index scannable. The palette is the semantic one the
// portal already uses rather than a set of raw hues, so it holds in both themes:
// reads are neutral, writes are the primary accent, and a delete is destructive.
const METHOD_CLASS: Record<string, string> = {
  GET: "bg-muted text-foreground/80",
  HEAD: "bg-muted text-foreground/70",
  OPTIONS: "bg-muted text-foreground/70",
  POST: "bg-primary/15 text-primary",
  PUT: "bg-primary/15 text-primary",
  PATCH: "bg-primary/15 text-primary",
  DELETE: "bg-destructive/15 text-destructive",
};

/** MethodBadge renders one HTTP method as a fixed-width chip. */
export function MethodBadge({ method, className }: { method: string; className?: string }) {
  const upper = method.toUpperCase();
  return (
    <span
      className={cn(
        "inline-block shrink-0 rounded px-1.5 py-0.5 text-center font-mono text-[10px] font-semibold uppercase leading-4",
        METHOD_CLASS[upper] ?? "bg-muted text-muted-foreground",
        className,
      )}
    >
      {upper}
    </span>
  );
}
