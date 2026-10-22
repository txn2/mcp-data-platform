import { useState } from "react";
import { Lock } from "lucide-react";
import {
  useAccessRequestPage,
  useRequestAccess,
  type AccessTargetType,
} from "@/api/portal/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

/**
 * NoAccess is what an item's page shows a signed-in person it does not open
 * (#2090): a request to its owner, with an optional message. An item that
 * does not exist, or one the person does open, falls back to `notFound`.
 */
export function NoAccess({
  type,
  id,
  notFound,
}: {
  type: AccessTargetType;
  id: string;
  notFound: React.ReactNode;
}) {
  const page = useAccessRequestPage(type, id);
  const request = useRequestAccess();
  const [message, setMessage] = useState("");
  if (page.isLoading) return null;
  if (!page.data || page.data.has_access) return <>{notFound}</>;
  const pending = page.data.pending ?? request.data;
  return (
    <EmptyState icon={Lock}>
      <p className="font-medium">
        {page.data.name || "You do not have access to this item"}
      </p>
      <p className="mt-1 text-sm text-muted-foreground">
        Owned by {page.data.owner_email}
      </p>
      {pending ? (
        <p className="mt-3 text-sm">
          Your request was sent to {page.data.owner_email}.
        </p>
      ) : (
        <div className="mx-auto mt-3 max-w-sm space-y-2 text-left">
          <Textarea
            aria-label="Message"
            placeholder="Message (optional)"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            rows={3}
          />
          {request.error && (
            <p className="text-xs text-destructive">{request.error.message}</p>
          )}
          <Button
            size="sm"
            disabled={request.isPending}
            onClick={() =>
              request.mutate({ type, id, message: message.trim() || undefined })
            }
          >
            Request access
          </Button>
        </div>
      )}
    </EmptyState>
  );
}
