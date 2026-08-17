import { useState } from "react";
import { PhoneCall } from "lucide-react";
import { useCall, usePromoteCall, useRejectCall } from "@/api/admin/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { CallDetailHeader } from "./CallDetailHeader";
import { CallDetailView } from "./CallDetailView";

// CallDetailPage is one recorded call opened by an operator: what ran, what it
// was for, what came of it, and the decision to publish or decline it.

export function CallDetailPage({
  callId,
  onNavigate,
  onBack,
}: {
  callId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}) {
  const { data, isLoading, error } = useCall(callId);
  const promote = usePromoteCall();
  const reject = useRejectCall();
  const [actionError, setActionError] = useState<string | undefined>();

  const header = (
    <CallDetailHeader
      record={data}
      backLabel="Calls"
      onBack={onBack}
      icon={PhoneCall}
    />
  );

  if (error) {
    return (
      <div className="space-y-4">
        {header}
        <EmptyState>
          This record does not exist, or the catalog no longer holds it.
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
          sessionPath={(sessionId) => `/admin/sessions/${encodeURIComponent(sessionId)}`}
          assetPath={(assetId) => `/admin/assets/${encodeURIComponent(assetId)}`}
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
