import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import "./stores/theme"; // Apply system theme before first paint
import { App } from "./App";

async function enableMocking() {
  if (import.meta.env.DEV && import.meta.env.VITE_MSW === "true") {
    // Auto-authenticate in MSW mode so the dashboard loads immediately
    const { useAuthStore } = await import("./stores/auth");
    useAuthStore.getState().setApiKey("msw-dev-key");

    const { worker } = await import("./mocks/browser");
    return worker.start({
      onUnhandledRequest: "bypass",
      serviceWorker: { url: "/mockServiceWorker.js" },
    });
  }
}

function render() {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}

enableMocking().then(render, render);
