import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import "./stores/theme"; // Apply system theme before first paint
import { App } from "./App";
import { installBuildWatch } from "./lib/buildWatch";

async function enableMocking() {
  if (import.meta.env.DEV && import.meta.env.VITE_MSW === "true") {
    const { worker } = await import("./mocks/browser");
    return worker.start({
      onUnhandledFrame: "bypass",
      serviceWorker: { url: "/mockServiceWorker.js" },
    });
  }
}

// Every answer names the build that served it; the page moves to a new one
// (#2727).
installBuildWatch();

function render() {
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
}

enableMocking().then(render, render);
