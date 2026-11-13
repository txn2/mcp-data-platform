import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { HandlerRow, HandlerType } from "@/api/portal/hooks/scriptHandlers";
import { REFETCH_INTERVAL } from "./shared";

// Admin > Automations > Handlers (#2435): every handler of every script.

export const adminHandlersKey = ["admin-handlers"] as const;

/** AdminHandlerListParams narrows the list: "" is no filter. */
export interface AdminHandlerListParams {
  type: "" | HandlerType;
  search: string;
  owner: string;
  state: "" | "on" | "off";
  page: number;
  perPage: number;
}

export interface AdminHandlerList {
  data: HandlerRow[];
  total: number;
}

/** handlerListQuery is the list route's query string for params. */
export function handlerListQuery(p: AdminHandlerListParams): string {
  const q = new URLSearchParams();
  if (p.type) q.set("type", p.type);
  if (p.search.trim()) q.set("search", p.search.trim());
  if (p.owner) q.set("owner", p.owner);
  if (p.state) q.set("enabled", String(p.state === "on"));
  q.set("limit", String(p.perPage));
  if (p.page > 1) q.set("offset", String((p.page - 1) * p.perPage));
  return q.toString();
}

/** useAdminHandlers reads one page of the handlers the filters match. */
export function useAdminHandlers(params: AdminHandlerListParams) {
  return useQuery({
    queryKey: [...adminHandlersKey, params],
    queryFn: () => apiFetch<AdminHandlerList>(`/handlers?${handlerListQuery(params)}`),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}
