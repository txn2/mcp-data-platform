import { useState, useEffect, useMemo, useCallback } from "react";
import { MessageSquare } from "lucide-react";
import { useMyPrompts, useUpdateMyPrompt, useDeleteMyPrompt, useCreateAsset, useSharedPrompts } from "@/api/portal/hooks";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { useAuthStore } from "@/stores/auth";
import { ShareDialog } from "@/components/ShareDialog";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { validatePromptName, isPromptNameConflict } from "./promptName";
import type { Prompt } from "@/api/admin/types";
import { extractPromptArguments } from "./promptArguments";
import { PromptViewerHeader } from "./viewer/PromptViewerHeader";
import { PromptEditForm } from "./viewer/PromptEditForm";
import { PromptReadView } from "./viewer/PromptReadView";
import { DeletePromptDialog } from "./viewer/DeletePromptDialog";
import { PromptNotices } from "./viewer/PromptNotices";
import { RequestPromotionDialog } from "./viewer/RequestPromotionDialog";
import type { EditForm, ViewMode } from "./viewer/types";

interface Props {
  promptId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}

export function PromptViewerPage({ promptId, onNavigate, onBack }: Props) {
  const { data, isLoading } = useMyPrompts();
  const { data: sharedData } = useSharedPrompts();
  const updateMutation = useUpdateMyPrompt();
  const deleteMutation = useDeleteMyPrompt();
  const createAssetMutation = useCreateAsset();
  const myEmail = useAuthStore((s) => s.user?.email) ?? "";

  const prompt = useMemo<Prompt | undefined>(() => {
    // Include prompts shared with the user so a "Shared With Me" prompt opens
    // here (it is not in the caller's own personal/available lists).
    const shared = (sharedData ?? []).map((s) => s.prompt);
    return [...(data?.personal ?? []), ...(data?.available ?? []), ...shared].find((p) => p.id === promptId);
  }, [data, sharedData, promptId]);

  // Owner = a personal prompt the current user owns. A prompt shared with the
  // user is also personal-scoped but owned by someone else, so it is read-only.
  const isOwner = prompt?.scope === "personal" && prompt?.owner_email === myEmail;

  const [viewMode, setViewMode] = useState<ViewMode>("preview");
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<EditForm>({ name: "", display_name: "", description: "", content: "", category: "", tags: [], arguments: [] });
  const [error, setError] = useState<string | null>(null);
  const [nameConflict, setNameConflict] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [saveAsAssetNotice, setSaveAsAssetNotice] = useState<{ assetId: string; name: string } | null>(null);
  const myPersona = useAuthStore((s) => s.user?.persona) ?? "";
  const [promoteOpen, setPromoteOpen] = useState(false);
  const [promoteScope, setPromoteScope] = useState<"persona" | "global">("persona");
  const [promoteError, setPromoteError] = useState<string | null>(null);

  const openPromote = useCallback(() => {
    setPromoteError(null);
    // Default to promoting into the user's own persona; fall back to global if
    // they are not assigned to one.
    setPromoteScope(myPersona ? "persona" : "global");
    setPromoteOpen(true);
  }, [myPersona]);
  const closePromote = useCallback(() => {
    setPromoteOpen(false);
    setPromoteError(null);
  }, []);

  // Reset edit form when prompt loads/changes.
  useEffect(() => {
    if (prompt && !editing) {
      setForm({
        name: prompt.name,
        display_name: prompt.display_name,
        description: prompt.description,
        content: prompt.content,
        category: prompt.category,
        tags: prompt.tags ?? [],
        arguments: prompt.arguments ?? [],
      });
    }
  }, [prompt, editing]);

  // Live-sync the arguments table with the content textarea: new {{name}}
  // placeholders typed in content appear as new rows; placeholders removed
  // from content drop out; descriptions and required flags the user has
  // edited in place are preserved across content edits.
  const handleContentChange = useCallback((next: string) => {
    setForm((prev) => ({
      ...prev,
      content: next,
      arguments: extractPromptArguments(next, prev.arguments),
    }));
  }, []);

  const updateArgField = useCallback(
    (name: string, patch: Partial<Prompt["arguments"][number]>) => {
      setForm((prev) => ({
        ...prev,
        arguments: prev.arguments.map((a) => (a.name === name ? { ...a, ...patch } : a)),
      }));
    },
    [],
  );

  const handleSave = useCallback(() => {
    if (!prompt) return;
    setError(null);
    setNameConflict(null);
    updateMutation.mutate(
      { id: prompt.id, ...form },
      {
        onSuccess: () => {
          setEditing(false);
        },
        onError: (err) => {
          const msg = err instanceof Error ? err.message : "Save failed";
          if (isPromptNameConflict(msg)) {
            setNameConflict("That name is already taken.");
          } else {
            setError(msg);
          }
        },
      },
    );
  }, [prompt, form, updateMutation]);

  const handleRequestPromotion = useCallback(() => {
    if (!prompt) return;
    setPromoteError(null);
    if (promoteScope === "persona" && !myPersona) {
      setPromoteError("You are not assigned to a persona; request global instead.");
      return;
    }
    updateMutation.mutate(
      { id: prompt.id, requested_scope: promoteScope, requested_personas: promoteScope === "persona" ? [myPersona] : [] },
      {
        onSuccess: () => closePromote(),
        onError: (err) => setPromoteError(err instanceof Error ? err.message : "Request failed"),
      },
    );
  }, [prompt, promoteScope, myPersona, updateMutation, closePromote]);

  const handleDelete = useCallback(() => {
    if (!prompt) return;
    setError(null);
    deleteMutation.mutate(prompt.id, {
      onSuccess: () => {
        setDeleteOpen(false);
        onBack();
      },
      onError: (err) => {
        setError(err instanceof Error ? err.message : "Delete failed");
      },
    });
  }, [prompt, deleteMutation, onBack]);

  const handleCopyContent = useCallback(async () => {
    if (!prompt) return;
    try {
      await navigator.clipboard.writeText(prompt.content);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // best-effort
    }
  }, [prompt]);

  const handleSaveAsAsset = useCallback(() => {
    if (!prompt) return;
    setError(null);
    const name = (prompt.display_name || prompt.name).trim() || "Prompt";
    const description = prompt.description || `Snapshot of prompt "${prompt.name}"`;
    createAssetMutation.mutate(
      {
        name,
        description,
        content_type: "text/markdown",
        content: prompt.content,
        tags: prompt.category ? ["prompt", prompt.category] : ["prompt"],
      },
      {
        onSuccess: (asset) => {
          setSaveAsAssetNotice({ assetId: asset.id, name: asset.name });
        },
        onError: (err) => {
          setError(err instanceof Error ? err.message : "Save as asset failed");
        },
      },
    );
  }, [prompt, createAssetMutation]);

  const handleShare = useCallback(() => {
    if (!prompt) return;
    setError(null);
    // Share the prompt natively: the recipient gets a real, runnable prompt
    // (served over MCP under its own name), not a markdown-asset snapshot. The
    // markdown export remains available as the separate "Save as Asset" action.
    setShareOpen(true);
  }, [prompt]);

  if (isLoading) {
    return <LoadingIndicator />;
  }

  if (!prompt) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
        <MessageSquare className="h-12 w-12 mb-2 opacity-30" />
        <p className="text-sm">Prompt not found</p>
        <button onClick={onBack} className="mt-2 text-sm text-primary hover:underline">Back</button>
      </div>
    );
  }

  const dirty = editing && (
    form.name !== prompt.name ||
    form.display_name !== prompt.display_name ||
    form.description !== prompt.description ||
    form.content !== prompt.content ||
    form.category !== prompt.category
  );

  return (
    <div className="space-y-4">
      <PromptViewerHeader
        prompt={prompt}
        editing={editing}
        isOwner={isOwner}
        copied={copied}
        createAssetPending={createAssetMutation.isPending}
        updatePending={updateMutation.isPending}
        saveDisabled={!dirty || updateMutation.isPending || validatePromptName(form.name) !== null}
        onBack={onBack}
        onCopyContent={handleCopyContent}
        onSaveAsAsset={handleSaveAsAsset}
        onShare={handleShare}
        onRequestPromotion={openPromote}
        onEdit={() => setEditing(true)}
        onDeleteRequest={() => setDeleteOpen(true)}
        onSave={handleSave}
        onCancel={() => { setEditing(false); setError(null); }}
      />

      <KnowledgeBacklinks urn={`mcp:prompt:${promptId}`} onNavigate={onNavigate} />

      {/* Notices */}
      <PromptNotices
        error={error}
        saveAsAssetNotice={saveAsAssetNotice}
        onOpenAsset={(assetId) => onNavigate(`/assets/${assetId}`)}
        onDismissNotice={() => setSaveAsAssetNotice(null)}
      />

      {editing ? (
        <PromptEditForm
          prompt={prompt}
          form={form}
          setForm={setForm}
          nameConflict={nameConflict}
          setNameConflict={setNameConflict}
          onContentChange={handleContentChange}
          updateArgField={updateArgField}
        />
      ) : (
        <PromptReadView prompt={prompt} viewMode={viewMode} setViewMode={setViewMode} />
      )}

      {/* Delete confirmation modal */}
      {deleteOpen && (
        <DeletePromptDialog
          prompt={prompt}
          pending={deleteMutation.isPending}
          onCancel={() => setDeleteOpen(false)}
          onConfirm={handleDelete}
        />
      )}

      {/* Native prompt share dialog */}
      <ShareDialog
        target={{ type: "prompt", id: prompt.id }}
        open={shareOpen}
        onOpenChange={setShareOpen}
      />

      {/* Request promotion dialog */}
      {promoteOpen && (
        <RequestPromotionDialog
          myPersona={myPersona}
          promoteScope={promoteScope}
          setPromoteScope={setPromoteScope}
          promoteError={promoteError}
          pending={updateMutation.isPending}
          onCancel={closePromote}
          onSubmit={handleRequestPromotion}
        />
      )}
    </div>
  );
}
