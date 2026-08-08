import { FileUp, FolderOpen, Loader2 } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { RESOURCE_POSITIONING } from "@/lib/positioning";
import type { Resource } from "@/api/resources/types";
import { ResourcesTable } from "./ResourcesTable";

// ResourceResults is what the library shows for the scope in view: the list, or
// the state standing in for it while it loads and when there is nothing to show.
export function ResourceResults({
  resources,
  isLoading,
  filtering,
  admin,
  onOpen,
  onUpload,
}: {
  resources: Resource[];
  isLoading: boolean;
  // Set when a filter is narrowing the view, which is a different emptiness
  // from a library nobody has uploaded to.
  filtering: boolean;
  admin: boolean;
  onOpen: (resource: Resource) => void;
  onUpload: () => void;
}) {
  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        <Loader2 className="mr-2 h-5 w-5 animate-spin" />
        Loading...
      </div>
    );
  }

  if (resources.length > 0) {
    return <ResourcesTable resources={resources} admin={admin} onOpen={onOpen} />;
  }

  // A filter that matched nothing is not an empty library, and saying so would
  // send someone off to upload a file they already have.
  if (filtering) {
    return (
      <EmptyState icon={FolderOpen} data-testid="resources-empty">
        <p className="font-medium text-foreground">No resources match this search</p>
      </EmptyState>
    );
  }

  return (
    <EmptyState
      icon={FolderOpen}
      data-testid="resources-empty"
      action={
        <Button onClick={onUpload}>
          <FileUp />
          Upload Resource
        </Button>
      }
    >
      <p className="font-medium text-foreground">No resources yet</p>
      <p className="mt-1 max-w-lg text-xs">{RESOURCE_POSITIONING}</p>
    </EmptyState>
  );
}
