import { useMemo } from "react";
import { useAuthStore } from "@/stores/auth";
import { resolvePortal, type ResolvedPortal } from "@/lib/portalAccess";

/**
 * The signed-in caller's portal permissions, every area resolved (#2225). The
 * store holds the block as /me sent it; resolving it here, once per change,
 * keeps a selector from returning a new object on every render.
 */
export function usePortalAccess(): ResolvedPortal {
  const portal = useAuthStore((s) => s.user?.portal);
  return useMemo(() => resolvePortal(portal), [portal]);
}
