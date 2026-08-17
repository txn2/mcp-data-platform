import type { LucideIcon } from "lucide-react";
import { PageHeader } from "@/components/patterns/PageHeader";
import type { CallRecord } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import { callTitle } from "./outcome";

/**
 * CallDetailHeader names the record being read: what it was for, and the
 * reference an agent cites it by.
 *
 * `showUser` carries the caller into the identity line for an operator; a
 * reader's own record drops it, the way the list drops its User column.
 */
export function CallDetailHeader({
  record,
  backLabel,
  onBack,
  icon,
  showUser = true,
}: {
  record?: CallRecord;
  backLabel: string;
  onBack: () => void;
  icon: LucideIcon;
  showUser?: boolean;
}) {
  return (
    <PageHeader
      backLabel={backLabel}
      onBack={onBack}
      icon={icon}
      title={record ? callTitle(record) : "Call"}
      urn={record?.reference}
      subtitle={record ? subtitleFor(record, showUser) : undefined}
    />
  );
}

/** The identity line: when, through what, and (for an operator) by whom. */
function subtitleFor(record: CallRecord, showUser: boolean): string {
  const parts = [new Date(record.created_at).toLocaleString(), record.tool_name];
  if (record.connection) {
    parts.push(record.connection);
  }
  if (showUser) {
    parts.push(formatUser(record.user_id ?? "", record.user_email));
  }
  return parts.join(" · ");
}
