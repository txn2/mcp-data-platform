import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { scriptsKey } from "./scriptKeys";

// A form's stored submissions (#2180): the newest rows of its webhook source,
// sensitive fields decrypted for the owner and the read audited (#2179).

export interface FormSubmitter {
  user_id: string;
  email: string;
  auth_type?: string;
}

export interface FormSubmission {
  id: string;
  received_at: string;
  submitter?: FormSubmitter;
  values: Record<string, unknown>;
}

export interface FormSubmissionsPage {
  form: string;
  source: string;
  table: string;
  connection: string;
  /** fields are the columns, in the order the form declares them. */
  fields: string[];
  /** sensitive are the fields that were decrypted for this read. */
  sensitive: string[];
  submissions: FormSubmission[];
  limit: number;
}

export const submissionsKey = (scriptID: string) => [...scriptsKey, scriptID, "submissions"];

// useScriptSubmissions reads a form's newest submissions.
export function useScriptSubmissions(scriptID: string, enabled: boolean) {
  return useQuery({
    queryKey: submissionsKey(scriptID),
    queryFn: () => apiFetch<FormSubmissionsPage>(`/scripts/${scriptID}/submissions`),
    enabled,
  });
}

/** submissionsExportURL is where a form's submissions download as format. */
export function submissionsExportURL(scriptID: string, format: "csv" | "xlsx"): string {
  return `/api/v1/portal/scripts/${encodeURIComponent(scriptID)}/submissions/export?format=${format}`;
}
