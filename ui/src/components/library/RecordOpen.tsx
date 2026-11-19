import { useRecordOpen, type LibraryKind } from "@/api/portal/hooks/library";

/** RecordOpen reports that the reader opened an item (#2504, #2509). A page
 *  renders it once the item has loaded, so an item the reader cannot open
 *  records nothing. */
export function RecordOpen({ kind, id }: { kind: LibraryKind; id: string }) {
  useRecordOpen(kind, id);
  return null;
}
