import { useState, useCallback } from "react";
import { useAPIKeys, useDeleteAPIKey, useSystemInfo } from "@/api/admin/hooks";
import type { APIKeyCreateResponse } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { Plus, KeyRound, ChevronUp } from "lucide-react";
import { PanelShell } from "./panels";
import { AddKeyForm } from "./keys/AddKeyForm";
import { CreatedKeyBanner } from "./keys/CreatedKeyBanner";
import { KeysTable } from "./keys/KeysTable";

// KeysPage lists the API keys that grant programmatic access and creates new
// ones. The created-key banner, the create form, the role browser, and the
// table live under ./keys/ (#1206) so this file owns only the page state.
export function KeysPage() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  const { data: keyList, isLoading } = useAPIKeys();
  const keys = keyList?.keys ?? [];

  const [showForm, setShowForm] = useState(false);
  const [createdKey, setCreatedKey] = useState<APIKeyCreateResponse | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const deleteMutation = useDeleteAPIKey();

  const handleCreated = useCallback((resp: APIKeyCreateResponse) => {
    setCreatedKey(resp);
    setShowForm(false);
  }, []);

  const handleDelete = useCallback(
    (name: string) => {
      deleteMutation.mutate(name, { onSuccess: () => setDeleteConfirm(null) });
    },
    [deleteMutation],
  );

  return (
    <PanelShell
      title="API Keys"
      description="Manage API keys for programmatic access"
      action={
        !isReadOnly && (
          <Button
            type="button"
            size="sm"
            variant={showForm ? "outline" : "default"}
            onClick={() => {
              setShowForm((prev) => !prev);
              setCreatedKey(null);
            }}
          >
            {showForm ? (
              <>
                <ChevronUp />
                Cancel
              </>
            ) : (
              <>
                <Plus />
                Add Key
              </>
            )}
          </Button>
        )
      }
    >
      {createdKey && (
        <CreatedKeyBanner
          response={createdKey}
          onDismiss={() => setCreatedKey(null)}
        />
      )}

      {showForm && <AddKeyForm onCreated={handleCreated} />}

      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <p className="py-16 text-center text-sm text-muted-foreground">
            Loading...
          </p>
        ) : keys.length === 0 ? (
          <div className="p-5">
            <EmptyState icon={KeyRound}>
              <p>No API keys configured</p>
              {!isReadOnly && (
                <p className="mt-1 text-xs">
                  Click &ldquo;Add Key&rdquo; to create one
                </p>
              )}
            </EmptyState>
          </div>
        ) : (
          <KeysTable
            keys={keys}
            isReadOnly={isReadOnly}
            deleteConfirm={deleteConfirm}
            deleting={deleteMutation.isPending}
            onRequestDelete={setDeleteConfirm}
            onCancelDelete={() => setDeleteConfirm(null)}
            onConfirmDelete={handleDelete}
          />
        )}
      </div>
    </PanelShell>
  );
}
