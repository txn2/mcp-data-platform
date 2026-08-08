import { useCallback, useState } from "react";
import { Check, Copy, X } from "lucide-react";
import type { APIKeyCreateResponse } from "@/api/admin/types";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// CreatedKeyBanner shows a freshly minted secret exactly once. It is a warning
// rather than a success notice: the secret is unrecoverable after dismissal,
// so the banner's job is to say "act now", not "well done".
export function CreatedKeyBanner({
  response,
  onDismiss,
}: {
  response: APIKeyCreateResponse;
  onDismiss: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    void navigator.clipboard.writeText(response.key).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [response.key]);

  return (
    <Alert variant="warning" className="rounded-none border-x-0 border-t-0 px-5 py-4">
      <AlertTitle className="line-clamp-none">
        Key created: {response.name} — copy it now, it will not be shown again
      </AlertTitle>
      <AlertDescription className="mt-2 flex w-full flex-row items-center gap-2">
        <code className="flex-1 break-all rounded-md border border-current/30 bg-card px-3 py-2 font-mono text-sm">
          {response.key}
        </code>
        <Button
          type="button"
          size="sm"
          onClick={handleCopy}
          className={cn(
            "shrink-0",
            copied
              ? "bg-emerald-600 text-white hover:bg-emerald-600"
              : "bg-amber-600 text-white hover:bg-amber-700",
          )}
        >
          {copied ? (
            <>
              <Check />
              Copied
            </>
          ) : (
            <>
              <Copy />
              Copy
            </>
          )}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="icon-sm"
          onClick={onDismiss}
          aria-label="Dismiss created key"
          className="shrink-0 border-current/30 text-current"
        >
          <X />
        </Button>
      </AlertDescription>
    </Alert>
  );
}
