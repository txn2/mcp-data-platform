import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useAuthStore } from "@/stores/auth";
import { LoginForm } from "@/components/LoginForm";
import { AppShell } from "@/components/layout/AppShell";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      staleTime: 10_000,
    },
  },
});

export function App() {
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated());

  return (
    <QueryClientProvider client={queryClient}>
      {isAuthenticated ? <AppShell /> : <LoginForm />}
    </QueryClientProvider>
  );
}
