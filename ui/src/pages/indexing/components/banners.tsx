import { AlertTriangle } from "lucide-react";

import { type IndexProviderStatus } from "@/api/admin/indexjobs";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ProviderBanner } from "./badges";

// errorSuffix renders whichever mutation failed as ": <reason>", or nothing
// when neither carried a message.
function errorSuffix(errors: unknown[]): string {
  const withMessage = errors.find((e) => e instanceof Error);
  return withMessage ? `: ${(withMessage as Error).message}` : "";
}

// IndexingBanners is everything the dashboard says before its panels: the
// embedding provider's state, a failed retry/dismiss, and the warning that the
// job detail behind most panels did not load.
export function IndexingBanners({
  provider,
  actionErrors,
  jobsFailed,
}: {
  provider?: IndexProviderStatus;
  // The reindex and dismiss mutation errors, in that order; empty when the
  // last action succeeded.
  actionErrors: unknown[];
  jobsFailed: boolean;
}) {
  return (
    <>
      {provider && (
        <ProviderBanner
          status={provider.status}
          kind={provider.kind}
          model={provider.model}
          dimension={provider.dimension}
        />
      )}

      {/* A failed action is the one banner here that follows from something
          the operator just did, so it stays an assertive alert; the polled
          load banners are status regions. */}
      {actionErrors.length > 0 && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertDescription>Action failed{errorSuffix(actionErrors)}.</AlertDescription>
        </Alert>
      )}

      {jobsFailed && (
        <Alert variant="warning" role="status">
          <AlertTriangle />
          <AlertDescription>
            Could not load job details; the throughput, latency, in-flight, and retry panels below
            may be incomplete.
          </AlertDescription>
        </Alert>
      )}
    </>
  );
}
