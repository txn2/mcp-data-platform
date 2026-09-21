import { useState, useCallback } from "react";
import { useTestConnectionInstance } from "@/api/admin/hooks";
import type { ConnectionTestResult } from "@/api/admin/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Plug } from "lucide-react";

// ConnectionTestButton opens a stored connection and reports what its upstream
// said.
//
// Before this, a connection could be created, listed and read back without
// anything ever having opened it, so an unusable connection looked exactly like
// a working one until a real call failed — which for a scheduled script is the
// middle of a pipeline. The result is deliberately verbose on success too: a
// green tick against the wrong credential looks the same as against the right
// one, so the panel says what answered.

// TESTABLE_KINDS are the kinds the test endpoint serves. It mirrors the
// server's own knownConnectionKinds minus mcp, which keeps the richer gateway
// test: a datahub connection is configured in the platform's YAML rather than
// managed as an instance, so the endpoint answers it "unknown connection kind"
// and a button offering the action would be a refusal with no path in.
const TESTABLE_KINDS = ["trino", "s3", "api", "graphql"];

// canTestConnection reports whether this kind can be opened on request.
export function canTestConnection(kind: string): boolean {
  return TESTABLE_KINDS.includes(kind);
}

export function ConnectionTestButton({
  kind,
  name,
}: {
  kind: string;
  name: string;
}) {
  const test = useTestConnectionInstance();
  const [result, setResult] = useState<ConnectionTestResult | null>(null);
  const [failed, setFailed] = useState<string | null>(null);

  const handleTest = useCallback(async () => {
    setResult(null);
    setFailed(null);
    try {
      setResult(await test.mutateAsync({ kind, name }));
    } catch (err) {
      setFailed(err instanceof Error ? err.message : "Test failed");
    }
  }, [kind, name, test]);

  return (
    <div className="space-y-3">
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => void handleTest()}
        disabled={test.isPending}
      >
        <Plug className="size-4" />
        {test.isPending ? "Testing…" : "Test connection"}
      </Button>

      {failed && (
        <Alert variant="destructive">
          <AlertTitle>Could not run the test</AlertTitle>
          <AlertDescription>{failed}</AlertDescription>
        </Alert>
      )}

      {result && (
        <Alert variant={result.ok ? "default" : "destructive"}>
          <AlertTitle>
            {result.ok ? "The connection answered" : "The connection did not answer"}
          </AlertTitle>
          <AlertDescription className="space-y-1">
            {result.detail && <p>{result.detail}</p>}
            {result.error && <p className="font-mono text-xs break-all">{result.error}</p>}
          </AlertDescription>
        </Alert>
      )}
    </div>
  );
}
