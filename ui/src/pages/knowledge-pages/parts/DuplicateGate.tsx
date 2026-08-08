import { Copy } from "lucide-react";
import type { KnowledgePageDuplicateResponse } from "@/api/portal/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

/**
 * DuplicateGate is what the create form shows when the backend blocked a create
 * because its content closely matches existing pages (#705). It is a warning
 * rather than an error: the reader either opens a candidate to consolidate onto
 * it, or says this really is a separate page.
 */
export function DuplicateGate({
  dup,
  pending,
  onOpenCandidate,
  onCreateAnyway,
  onDismiss,
}: {
  dup: KnowledgePageDuplicateResponse;
  pending: boolean;
  onOpenCandidate: (id: string) => void;
  onCreateAnyway: () => void;
  onDismiss: () => void;
}) {
  return (
    <Alert variant="warning">
      <Copy />
      <AlertTitle>Similar pages already exist</AlertTitle>
      <AlertDescription className="space-y-2">
        <p>
          Update existing knowledge instead of creating a duplicate. Open a page below to
          consolidate onto it, or create a separate page anyway.
        </p>
        <ul className="space-y-1">
          {dup.candidates.map((c) => (
            <li key={c.id}>
              <Button
                variant="link"
                size="xs"
                className="h-auto p-0"
                onClick={() => onOpenCandidate(c.id)}
              >
                {c.title}
                {c.slug ? <span className="opacity-70">({c.slug})</span> : null}
              </Button>
              <span className="ml-2 text-xs opacity-70">{Math.round(c.score * 100)}% match</span>
            </li>
          ))}
        </ul>
        <div className="flex items-center gap-2 pt-1">
          <Button variant="outline" size="xs" onClick={onCreateAnyway} disabled={pending}>
            Create separate page anyway
          </Button>
          <Button variant="ghost" size="xs" onClick={onDismiss}>
            Dismiss
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  );
}
