import { TriangleAlert } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";

/**
 * IncompleteBadge marks a file an export cut at a row or page limit and wrote
 * anyway (#2057), so whoever opens it later, without the tool response, sees
 * that it is partial. The title carries the detail when it is known.
 */
export function IncompleteBadge({ title, className }: { title?: string; className?: string }) {
  return (
    <Badge variant="warning" className={className} title={title ?? "Incomplete: an export limit cut this file"}>
      <TriangleAlert />
      Incomplete
    </Badge>
  );
}

/**
 * IncompleteNotice is the same mark at the top of a file's sidebar, with the
 * limit when the current version records it, and what that means for a reader.
 */
export function IncompleteNotice({ label }: { label: string }) {
  return (
    <Alert variant="warning" data-testid="incomplete-notice">
      <TriangleAlert />
      <AlertTitle>{label}</AlertTitle>
      <AlertDescription>
        The export that wrote this file stopped at a limit while the source had more, so the file holds only part of it.
      </AlertDescription>
    </Alert>
  );
}
