import {
  useAccessRequests,
  useDecideAccessRequest,
  type AccessTargetType,
} from "@/api/portal/hooks";
import { Button } from "@/components/ui/button";

/**
 * AccessRequestsList is the requests for access waiting on the reader (#2090),
 * all of them or one item's, each answered in one step.
 */
export function AccessRequestsList({
  target,
}: {
  target?: { type: AccessTargetType; id: string };
}) {
  const { data } = useAccessRequests(target);
  const decide = useDecideAccessRequest();
  const requests = data?.requests ?? [];
  if (requests.length === 0) return null;
  return (
    <section
      id="access-requests"
      aria-labelledby="access-requests-heading"
      className="space-y-2"
    >
      <h3 id="access-requests-heading" className="text-sm font-medium">
        Access requests
      </h3>
      <ul className="space-y-2">
        {requests.map((r) => (
          <li key={r.id} className="rounded-md border p-3 text-sm">
            <p>
              <span className="font-medium">{r.requester_email}</span>
              {!target && r.target_name ? (
                <span className="text-muted-foreground">
                  {" "}
                  · {r.target_name}
                </span>
              ) : null}
            </p>
            {r.message && (
              <p className="mt-1 text-muted-foreground">{r.message}</p>
            )}
            <div className="mt-2 flex flex-wrap gap-2">
              <Button
                size="xs"
                disabled={decide.isPending}
                onClick={() => decide.mutate({ id: r.id, decision: "viewer" })}
              >
                Approve as viewer
              </Button>
              <Button
                size="xs"
                variant="outline"
                disabled={decide.isPending}
                onClick={() => decide.mutate({ id: r.id, decision: "editor" })}
              >
                Approve as editor
              </Button>
              <Button
                size="xs"
                variant="ghost"
                disabled={decide.isPending}
                onClick={() => decide.mutate({ id: r.id, decision: "decline" })}
              >
                Decline
              </Button>
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}
