import { useEffect, useState } from "react";
import { FileWarning } from "lucide-react";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import { resolveRenderer, exceedsInlineLimit } from "@/components/renderers/registry";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { resourceFetchRaw, BASE_URL } from "@/api/resources/client";
import { formatBytes } from "@/lib/format";
import type { Resource } from "@/api/resources/types";

/**
 * Inline preview of a managed resource, through the shared renderer registry.
 *
 * Resources are view-only here: uploads are replaced through the existing edit
 * flow, which keeps the deny-list validation on the server side of every write.
 * The preview exists so someone can confirm what a resource holds without
 * downloading it first, which is the whole reason the detail view is open.
 *
 * Content is fetched through the resources API client rather than by pointing
 * an element at the endpoint, because that is the one path that carries the
 * session's credential regardless of whether it is a cookie or an API key.
 */
export function ResourcePreview({ resource }: { resource: Resource }) {
  const entry = resolveRenderer({ contentType: resource.mime_type, fileName: resource.filename });
  const tooLarge = exceedsInlineLimit(resource.mime_type, resource.size_bytes, resource.filename);

  const [text, setText] = useState<string | undefined>(undefined);
  const [objectUrl, setObjectUrl] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (tooLarge) {
      setLoading(false);
      return;
    }

    let cancelled = false;
    let created: string | null = null;
    setLoading(true);
    setError(null);
    setText(undefined);
    setObjectUrl("");

    resourceFetchRaw(`/${resource.id}/content`)
      .then(async (res) => {
        if (!res.ok) throw new Error(`Failed to load content (${res.status})`);
        if (entry.source === "url") {
          const blob = await res.blob();
          if (cancelled) return;
          created = URL.createObjectURL(blob);
          setObjectUrl(created);
          return;
        }
        const body = await res.text();
        if (!cancelled) setText(body);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : "Failed to load content");
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
      if (created) URL.revokeObjectURL(created);
    };
  }, [resource.id, entry.source, tooLarge]);

  if (tooLarge) {
    return (
      <PreviewFrame>
        <div className="flex flex-col items-center gap-2 py-8 text-center text-sm text-muted-foreground">
          <FileWarning aria-hidden className="size-8" />
          <p>
            This resource is {formatBytes(resource.size_bytes)}, past the inline preview limit. Download it to view.
          </p>
        </div>
      </PreviewFrame>
    );
  }

  if (loading) {
    return (
      <PreviewFrame>
        <LoadingIndicator />
      </PreviewFrame>
    );
  }

  if (error) {
    return (
      <PreviewFrame>
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      </PreviewFrame>
    );
  }

  return (
    <PreviewFrame>
      <ContentRenderer
        contentType={resource.mime_type}
        content={text}
        fileName={resource.filename}
        contentUrl={objectUrl || `${BASE_URL}/${resource.id}/content`}
        sizeBytes={resource.size_bytes}
      />
    </PreviewFrame>
  );
}

function PreviewFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <span className="text-xs font-medium text-muted-foreground">Preview</span>
      <div className="max-h-[50vh] overflow-auto rounded-md border bg-muted p-2">{children}</div>
    </div>
  );
}
