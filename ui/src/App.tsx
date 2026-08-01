import { useEffect } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAuthStore } from "@/stores/auth";
import { LoginForm } from "@/components/LoginForm";
import { AccessDenied } from "@/components/AccessDenied";
import { AppShell } from "@/components/layout/AppShell";
import { LoadingIndicator } from "@/components/LoadingIndicator";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        // Don't retry on 401 — the session is expired.
        if (error && "status" in error && (error as { status: number }).status === 401) {
          return false;
        }
        return failureCount < 1;
      },
      staleTime: 10_000,
    },
  },
});

function AuthGate() {
  const user = useAuthStore((s) => s.user);
  const loading = useAuthStore((s) => s.loading);
  const accessDenied = useAuthStore((s) => s.accessDenied);
  const checkSession = useAuthStore((s) => s.checkSession);

  useEffect(() => {
    checkSession();
  }, [checkSession]);

  if (loading) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <LoadingIndicator />
      </div>
    );
  }

  if (user) return <AppShell />;
  // Authenticated but unmapped is checked before the login form: both states
  // have a null user, and offering sign-in to someone the IdP already accepted
  // just loops them back here.
  return accessDenied ? <AccessDenied /> : <LoginForm />;
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <AuthGate />
    </QueryClientProvider>
  );
}
