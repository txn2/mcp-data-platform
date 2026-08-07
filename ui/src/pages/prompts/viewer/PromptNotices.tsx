import { X } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { FormError } from "../primitives";

// PromptNotices renders the inline error banner and the "saved as asset"
// success banner shown above the prompt body.
export function PromptNotices({
  error,
  saveAsAssetNotice,
  onOpenAsset,
  onDismissNotice,
}: {
  error: string | null;
  saveAsAssetNotice: { assetId: string; name: string } | null;
  onOpenAsset: (assetId: string) => void;
  onDismissNotice: () => void;
}) {
  return (
    <>
      <FormError message={error} />
      {saveAsAssetNotice && (
        <Alert variant="success">
          <AlertDescription className="flex w-full flex-row items-center justify-between gap-3 text-xs">
            <span>Saved as asset “{saveAsAssetNotice.name}”.</span>
            <span className="flex items-center gap-1">
              <Button
                variant="link"
                size="xs"
                className="text-current"
                onClick={() => onOpenAsset(saveAsAssetNotice.assetId)}
              >
                Open asset
              </Button>
              <Button
                variant="ghost"
                size="icon-xs"
                className="text-current"
                onClick={onDismissNotice}
                aria-label="Dismiss"
              >
                <X />
              </Button>
            </span>
          </AlertDescription>
        </Alert>
      )}
    </>
  );
}
