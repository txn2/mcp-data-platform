import { useState } from "react";
import { useAuthStore } from "@/stores/auth";

export function LoginForm() {
  const [key, setKey] = useState("");
  const setApiKey = useAuthStore((s) => s.setApiKey);

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (key.trim()) {
      setApiKey(key.trim());
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-muted/40">
      <form
        onSubmit={handleSubmit}
        className="w-full max-w-sm rounded-lg border bg-card p-6 shadow-sm"
      >
        <h1 className="mb-1 text-xl font-semibold">MCP Data Platform</h1>
        <p className="mb-4 text-sm text-muted-foreground">
          Enter your API key to access the admin dashboard.
        </p>
        <input
          type="password"
          value={key}
          onChange={(e) => setKey(e.target.value)}
          placeholder="API Key"
          className="mb-3 w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          autoFocus
        />
        <button
          type="submit"
          disabled={!key.trim()}
          className="w-full rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          Sign In
        </button>
      </form>
    </div>
  );
}
