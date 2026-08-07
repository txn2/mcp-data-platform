import { Code, File, FileText, Image, Table2, type LucideIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * The content families the portal distinguishes when it shows what an asset is.
 * A family, not a media type: `text/csv` and `application/csv` are one thing to
 * a reader, and the icon and the tint have to agree about which.
 */
type Family = "tabular" | "component" | "markup" | "image" | "text" | "other";

const FAMILY_ICON: Record<Family, LucideIcon> = {
  tabular: Table2,
  component: Code,
  markup: Code,
  image: Image,
  text: FileText,
  other: File,
};

// The families ride ui/badge's semantic variants rather than restating tints.
// Six families over five variants means the tint narrows the guess and the icon
// settles it; nothing here is a status, so no family takes `danger`.
const FAMILY_VARIANT: Record<Family, "success" | "info" | "warning" | "secondary" | "outline" | "muted"> = {
  tabular: "success",
  component: "info",
  markup: "warning",
  image: "secondary",
  text: "outline",
  other: "muted",
};

function contentTypeFamily(contentType: string): Family {
  const lower = contentType.toLowerCase();
  if (lower.includes("csv")) return "tabular";
  if (lower.includes("jsx") || lower.includes("react")) return "component";
  if (lower.includes("html")) return "markup";
  if (lower.includes("svg") || lower.includes("image")) return "image";
  if (lower.includes("markdown") || lower.includes("text")) return "text";
  return "other";
}

/** The icon standing for a content type wherever an asset is listed. */
export function contentTypeIcon(contentType: string): LucideIcon {
  return FAMILY_ICON[contentTypeFamily(contentType)];
}

/**
 * ContentTypeBadge is the pill naming what an asset is. It carries the raw
 * media type as its text — the reader who cares wants the exact string — and
 * the family only in its tint.
 */
export function ContentTypeBadge({
  contentType,
  className,
}: {
  contentType: string;
  className?: string;
}) {
  return (
    <Badge
      variant={FAMILY_VARIANT[contentTypeFamily(contentType)]}
      className={cn("px-1.5", className)}
    >
      {contentType}
    </Badge>
  );
}
