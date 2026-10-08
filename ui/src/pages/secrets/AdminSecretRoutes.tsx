import { SecretEditor } from "./SecretEditor";
import { SecretsPage } from "./SecretsPage";

// AdminSecretRoutes is the Secrets section (#2051): the list, a new secret,
// and one secret's editor. It owns its route matching, as the other
// sections do.

const NEW_ROUTE = "/admin/secrets/new";
const EDIT_ROUTE = /^\/admin\/secrets\/([^/]+)$/;

export function AdminSecretRoutes({
  route,
  onNavigate,
  onBack,
}: {
  route: string;
  onNavigate: (path: string, opts?: { replace?: boolean }) => void;
  onBack: (fallback: string) => void;
}) {
  const toList = () => onBack("/admin/secrets");
  const saved = () => onNavigate("/admin/secrets", { replace: true });
  if (route === "/admin/secrets")
    return <SecretsPage onNavigate={onNavigate} />;
  if (route === NEW_ROUTE)
    return <SecretEditor onBack={toList} onSaved={saved} />;
  const edit = route.match(EDIT_ROUTE);
  if (!edit) return null;
  return (
    <SecretEditor
      name={decodeURIComponent(edit[1]!)}
      onBack={toList}
      onSaved={saved}
    />
  );
}
