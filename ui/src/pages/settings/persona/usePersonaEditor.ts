import { useState, useCallback, useEffect } from "react";
import { useCreatePersona, useUpdatePersona, useEffectiveAccess } from "@/api/admin/hooks";
import type { PersonaCreateRequest } from "@/api/admin/types";
import type { PersonaDraft } from "./types";

// contextPayload is the draft's description and instruction overrides, each
// sent only when written.
function contextPayload(draft: PersonaDraft) {
  return {
    description_prefix: draft.descriptionPrefix || undefined,
    description_override: draft.descriptionOverride || undefined,
    agent_instructions_suffix: draft.agentInstructionsSuffix || undefined,
    agent_instructions_override: draft.agentInstructionsOverride || undefined,
  };
}

// portalPayload is the stored portal block as the editor shows it: every area,
// so nothing resolves from the persona's tools on the server. external is
// reported, never set, and uploads are an external persona's alone (#2258).
function portalPayload(draft: PersonaDraft) {
  const { external: _external, uploads, ...areas } = draft.portal;
  return draft.external && uploads ? { ...areas, uploads } : areas;
}

// toPayload converts the flat editor draft into the admin API body. Empty
// strings and empty connection lists are sent as undefined rather than as
// empty values: the API treats an absent field as "unset", and a persona with
// no connection rules must not be saved as one that denies every connection.
export function toPayload(draft: PersonaDraft): PersonaCreateRequest {
  return {
    name: draft.name,
    display_name: draft.displayName,
    description: draft.description || undefined,
    roles: draft.roles,
    allow_tools: draft.allowTools,
    deny_tools: draft.denyTools,
    allow_connections: draft.allowConnections.length > 0 ? draft.allowConnections : undefined,
    deny_connections: draft.denyConnections.length > 0 ? draft.denyConnections : undefined,
    // Sent only when the persona has rules. An absent field leaves the persona
    // with none, which is the same "no rule names this connection" state a
    // persona that never had any is in.
    api_routes: draft.apiRoutes.length > 0 ? draft.apiRoutes : undefined,
    priority: draft.priority,
    ...contextPayload(draft),
    // The account type is one of three (#2257): the two flags are never both
    // set.
    service_account: (draft.serviceAccount && !draft.external) || undefined,
    portal: portalPayload(draft),
    external: draft.external || undefined,
    // Sent every time: absent allows sharing, so a persona denied it has to
    // say so, and one allowed it says so to undo a denial.
    allow_sharing: draft.allowSharing,
  };
}

// useDebounced is value once it has stopped changing for ms.
function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return settled;
}

// usePersonaEditor owns what the persona editor knows that is not layout: the
// server's evaluation of the unsaved draft, which every section reads, and
// the save.
export function usePersonaEditor({
  draft,
  onSave,
  isCreate,
  selectedName,
}: {
  draft: PersonaDraft;
  onSave: () => void;
  isCreate: boolean;
  selectedName: string | null;
}) {
  const createMut = useCreatePersona();
  const updateMut = useUpdatePersona();
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // The summary follows the draft a moment after typing stops, rather than a
  // request per keystroke.
  const settled = useDebounced(draft, 250);
  const effective = useEffectiveAccess(toPayload(settled));

  const handleSave = useCallback(() => {
    setSaveError(null);
    const payload = toPayload(draft);
    const mutation = isCreate ? createMut : updateMut;
    mutation.mutate(isCreate ? payload : { ...payload, name: selectedName ?? "" }, {
      onSuccess: () => {
        setSaveSuccess(true);
        setTimeout(() => setSaveSuccess(false), 2000);
        onSave();
      },
      onError: (err) => {
        setSaveError(err instanceof Error ? err.message : "Failed to save");
      },
    });
  }, [draft, isCreate, selectedName, createMut, updateMut, onSave]);

  return {
    report: effective.data,
    reportLoading: effective.isLoading,
    handleSave,
    isPending: createMut.isPending || updateMut.isPending,
    saveSuccess,
    saveError,
  };
}
