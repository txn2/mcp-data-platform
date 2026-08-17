import { useState } from "react";
import { PhoneCall } from "lucide-react";
import { useMyCall, usePromoteMyCall, useRejectMyCall } from "@/api/portal/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { CallDetailHeader } from "@/pages/calls/CallDetailHeader";
import { CallDetailView } from "@/pages/calls/CallDetailView";
import { mySessionPath } from "./routes";

// MyCallDetailPage is one of the reader's own recorded calls opened: what ran,
// what it was for, what came of it, and the choice to publish it.
//
// A record that is not the reader's own is not found rather than refused, so
// the empty state covers both a record that aged out and an id that was never
// theirs — deliberately, since distinguishing them would be an answer about
// someone else's work.

export function MyCallDetailPage({
  callId,
  onNavigate,
  onBack,
}: {
  callId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}) {
  const { data, isLoading, error } = useMyCall(callId);
  const promote = usePromoteMyCall();
  const reject = useRejectMyCall();
  const [actionError, setActionError] = useState<string | undefined>();

  const header = (
    <CallDetailHeader
      record={data}
      backLabel="My Calls"
      onBack={onBack}
      icon={PhoneCall}
      showUser={false}
    />
  );

  if (error) {
    return (
      <div className="space-y-4">
        {header}
        <EmptyState icon={PhoneCall}>
          No call of yours is recorded under this id. It may have aged out of
          the catalog.
        </EmptyState>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {header}
      {isLoading || !data ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <CallDetailView
          record={data}
          showUser={false}
          sessionPath={mySessionPath}
          assetPath={(assetId) => `/assets/${assetId}`}
          onNavigate={onNavigate}
          isActing={promote.isPending || reject.isPending}
          actionError={actionError}
          onPromote={() => {
            setActionError(undefined);
            promote.mutate(data.id, {
              onError: (err: Error) => setActionError(err.message),
            });
          }}
          onReject={(note) => {
            setActionError(undefined);
            reject.mutate(
              { id: data.id, note },
              { onError: (err: Error) => setActionError(err.message) },
            );
          }}
        />
      )}
    </div>
  );
}
