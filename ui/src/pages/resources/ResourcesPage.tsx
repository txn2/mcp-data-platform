import { useMemo } from "react";
import { useAuthStore } from "@/stores/auth";
import { usePersonas } from "@/api/admin/hooks";
import { FileManager } from "./browser/FileManager";
import { isPlatformAdmin } from "./scopes";

interface Props {
  admin?: boolean;
  /**
   * The in-app location the shell is showing, query string included. The
   * top-level folder and the folder in view are read out of it (#1530), so a
   * reload, a Back, and a pasted link all land on the same place.
   */
  location: string;
  onNavigate?: (path: string, opts?: { replace?: boolean }) => void;
}

/**
 * The Resources page: a file manager over every folder the caller can read
 * (#1872). The same page serves the portal and the admin section; the admin
 * one adds a People folder and a Last read column.
 */
export function ResourcesPage({ admin = false, location, onNavigate }: Props) {
  const user = useAuthStore((s) => s.user);
  // The deployment's persona list, fetched for a platform administrator only:
  // their authority covers every persona, and their own claims name only the
  // personas they belong to (#1527). Everyone else's personas come from their
  // claims.
  const { data: personaData } = usePersonas(isPlatformAdmin(user));
  const personaNames = useMemo(() => (personaData?.personas ?? []).map((p) => p.name), [personaData]);
  return (
    <FileManager
      admin={admin}
      user={user}
      personaNames={personaNames}
      basePath={admin ? "/admin/resources" : "/resources"}
      location={location}
      onNavigate={onNavigate}
    />
  );
}
