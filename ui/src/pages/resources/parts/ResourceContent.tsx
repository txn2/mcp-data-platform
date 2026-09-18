import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { FileWarning } from "lucide-react";
import { ContentRenderer } from "@/components/renderers/ContentRenderer";
import {
  resolveRenderer,
  exceedsInlineLimit,
  isEditableContent,
} from "@/components/renderers/registry";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  SaveControls,
  ViewModeToggle,
} from "@/components/assetviewer/contentControls";
import type { ViewMode } from "@/components/assetviewer/types";
import { resourceFetchRaw, BASE_URL } from "@/api/resources/client";
import { useReplaceContent } from "@/api/resources/hooks";
import { formatBytes } from "@/lib/format";
import type { Resource } from "@/api/resources/types";

const SourceEditor = lazy(() =>
  import("@/components/SourceEditor").then((m) => ({
    default: m.SourceEditor,
  })),
);

/**
 * editSummary is what a version written from this editor says about itself, so
 * the version panel can tell an edit from a file picked off disk.
 */
const editSummary = "edited in the portal";

/** A resource's content, once it has been read. */
interface ResourceBody {
  /** The content as text, for a family the renderer embeds. */
  text: string | undefined;
  /** An object URL, for a family whose renderer streams from one. */
  objectUrl: string;
  loading: boolean;
  error: string | null;
  /** Replaces the loaded text once a save has written new bytes. */
  setText: (v: string) => void;
}

/**
 * useResourceBody reads a resource's content and reports what the viewer needs
 * to draw it.
 *
 * It fetches through the resources API client rather than by pointing an
 * element at the endpoint, because that is the one path carrying the session's
 * credential regardless of whether it is a cookie or an API key.
 *
 * onLoaded is called with the text each time the STORED content arrives -- the
 * first read, and a re-read after the resource changes -- and never when a save
 * replaces it, so a save can set the served text without clearing the result it
 * is reporting. It is read through a ref, so the effect does not re-run when
 * the caller rebuilds it.
 */
function useResourceBody(
  resource: Resource,
  tooLarge: boolean,
  fromURL: boolean,
  onLoaded: (text: string) => void,
): ResourceBody {
  const [text, setText] = useState<string | undefined>(undefined);
  const [objectUrl, setObjectUrl] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadedRef = useRef(onLoaded);
  loadedRef.current = onLoaded;

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
        if (fromURL) {
          const blob = await res.blob();
          if (cancelled) return;
          created = URL.createObjectURL(blob);
          setObjectUrl(created);
          return;
        }
        const body = await res.text();
        if (cancelled) return;
        setText(body);
        loadedRef.current(body);
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError(
            err instanceof Error ? err.message : "Failed to load content",
          );
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
      if (created) URL.revokeObjectURL(created);
    };
  }, [resource.id, fromURL, tooLarge]);

  return { text, objectUrl, loading, error, setText };
}

/** The draft being edited, and what writing it back does. */
interface SourceDraft {
  content: string;
  hasChanges: boolean;
  saveStatus: "idle" | "saved" | "error";
  pending: boolean;
  error: unknown;
  onChange: (v: string) => void;
  onSave: () => void;
  /** Seeds the draft from the stored bytes each time they are read. */
  reset: (loaded: string) => void;
}

/**
 * useSourceDraft holds the edited text and writes it back as the file's next
 * version.
 *
 * The save posts through the same replace-content route the version panel's
 * file picker uses, so it records a version, keeps the resource's id, URI and
 * filename, and carries the server-side validation of a write. It names itself
 * on the version it writes, which is how the trail tells an edit from a file
 * picked off disk.
 */
function useSourceDraft(resource: Resource, body: ResourceBody): SourceDraft {
  const [content, setContent] = useState<string>("");
  const [dirty, setDirty] = useState(false);
  const [saveStatus, setSaveStatus] = useState<"idle" | "saved" | "error">(
    "idle",
  );
  const replace = useReplaceContent();
  const { setText } = body;

  const reset = useCallback((loaded: string) => {
    setContent(loaded);
    setDirty(false);
    setSaveStatus("idle");
  }, []);

  const onChange = useCallback((v: string) => {
    setContent(v);
    setDirty(true);
  }, []);

  const onSave = useCallback(() => {
    setSaveStatus("idle");
    // The resource's own filename and type: the route ignores the uploaded
    // part's name deliberately, because a revision that changed the URI would
    // break every mcp:resource:<id> citation pointing at it.
    const file = new File([content], resource.filename, {
      type: resource.mime_type || "text/plain",
    });
    replace.mutate(
      { id: resource.id, file, changeSummary: editSummary },
      {
        onSuccess: () => {
          setSaveStatus("saved");
          setText(content);
          setDirty(false);
          // The view stays where the person put it. Dropping back to Preview
          // would take the result of the save off the screen with it -- the
          // Saved badge sits in the row the editor's controls are in -- and
          // somebody who opened Source is usually still editing.
        },
        onError: () => setSaveStatus("error"),
      },
    );
  }, [
    content,
    replace,
    resource.filename,
    resource.id,
    resource.mime_type,
    setText,
  ]);

  return {
    content,
    hasChanges: dirty && content !== (body.text ?? ""),
    saveStatus,
    pending: replace.isPending,
    error: replace.error,
    onChange,
    onSave,
    reset,
  };
}

/**
 * A managed resource's content, at the width of the page it opens on, editable
 * in place by whoever may change the file.
 *
 * It renders through the shared renderer registry -- the same one the asset
 * viewer uses -- and, like the asset viewer's content region, sets no height of
 * its own: the portal's page area is the scroll region, so a wide CSV scrolls
 * horizontally inside its own table and the page scrolls vertically once. In
 * the dialog this replaced, the same renderer drew a 512-pixel-wide table
 * inside a box half the viewport tall inside a scrolling column (#1470).
 *
 * The Preview/Source switch, the editor and the Save control are the asset
 * viewer's own, imported rather than rewritten, so the two surfaces cannot
 * drift: a resource and an asset are the same kind of object -- a stored file
 * with a content type and a version trail -- and a person editing one should
 * not have to learn a second set of controls (#1775).
 *
 * Until this, a resource was view-only here and its bytes could only be changed
 * by picking a whole replacement file off disk. The Edit button beside Download
 * opened the metadata dialog, so somebody looking at their CSV pressed the only
 * control named Edit and could not touch the file.
 */
export function ResourceContent({
  resource,
  canModify = false,
}: {
  resource: Resource;
  canModify?: boolean;
}) {
  const entry = resolveRenderer({
    contentType: resource.mime_type,
    fileName: resource.filename,
  });
  const tooLarge = exceedsInlineLimit(
    resource.mime_type,
    resource.size_bytes,
    resource.filename,
  );

  const [viewMode, setViewMode] = useState<ViewMode>("preview");
  const draftRef = useRef<SourceDraft | null>(null);
  const body = useResourceBody(
    resource,
    tooLarge,
    entry.source === "url",
    (loaded) => draftRef.current?.reset(loaded),
  );
  const draft = useSourceDraft(resource, body);
  draftRef.current = draft;

  const canEditSource = editableHere(resource, canModify, tooLarge);

  if (tooLarge) {
    return <ResourceTooLarge sizeBytes={resource.size_bytes} />;
  }
  if (body.loading) {
    return <LoadingIndicator />;
  }
  if (body.error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{body.error}</AlertDescription>
      </Alert>
    );
  }

  return (
    <div
      data-testid="resource-content"
      className="flex min-h-0 flex-1 flex-col gap-2"
    >
      {canEditSource && (
        <div className="flex flex-wrap items-center gap-2">
          <ViewModeToggle
            show
            viewMode={viewMode}
            onSetViewMode={setViewMode}
          />
          <SaveControls
            show={viewMode === "source"}
            hasChanges={draft.hasChanges}
            saveStatus={draft.saveStatus}
            onSaveContent={draft.onSave}
            pending={draft.pending}
          />
          <SaveError error={draft.error} />
        </div>
      )}
      <ResourceBodyView
        resource={resource}
        body={body}
        draft={draft}
        editing={canEditSource && viewMode === "source"}
      />
    </div>
  );
}

/**
 * editableHere answers whether this file's source can be edited on this page: a
 * reader who may change it, a family the editor can represent, and a file small
 * enough to load into one. A file past the inline preview limit is replaced
 * rather than edited.
 */
function editableHere(
  resource: Resource,
  canModify: boolean,
  tooLarge: boolean,
): boolean {
  if (!canModify || tooLarge) return false;
  return isEditableContent(resource.mime_type, resource.filename);
}

/** The editor, or the rendered content. */
function ResourceBodyView({
  resource,
  body,
  draft,
  editing,
}: {
  resource: Resource;
  body: ResourceBody;
  draft: SourceDraft;
  editing: boolean;
}) {
  if (editing) {
    return (
      <Suspense fallback={<LoadingIndicator />}>
        <SourceEditor
          content={draft.content}
          contentType={resource.mime_type}
          fileName={resource.filename}
          onChange={draft.onChange}
        />
      </Suspense>
    );
  }
  return (
    <ContentRenderer
      contentType={resource.mime_type}
      content={draft.hasChanges ? draft.content : body.text}
      fileName={resource.filename}
      contentUrl={body.objectUrl || `${BASE_URL}/${resource.id}/content`}
      sizeBytes={resource.size_bytes}
    />
  );
}

/** Why a save was refused, in the words the server used. */
function SaveError({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <span className="text-xs text-destructive">
      {error instanceof Error ? error.message : "Save failed"}
    </span>
  );
}

/** The size guard: a file past its family's inline cutoff is downloaded. */
function ResourceTooLarge({ sizeBytes }: { sizeBytes: number }) {
  return (
    <div
      data-testid="resource-content-too-large"
      className="flex flex-col items-center gap-2 rounded-md border py-12 text-center text-sm text-muted-foreground"
    >
      <FileWarning aria-hidden className="size-8" />
      <p>
        This resource is {formatBytes(sizeBytes)}, past the inline preview
        limit. Download it to view.
      </p>
    </div>
  );
}
