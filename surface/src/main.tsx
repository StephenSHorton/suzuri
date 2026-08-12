import {
  createHashHistory,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import "./index.css";
import { isMac } from "@/lib/platform";
import { routeTree } from "./routeTree.gen";

// Hash history: reliable in Tauri (no server-side path rewrites needed).
const history = createHashHistory();
const router = createRouter({ routeTree, history });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

// Transparent corners for rounded frameless window on macOS
if (isMac) document.documentElement.classList.add("mac-round");

const rootElement = document.getElementById("root");
if (rootElement && !rootElement.innerHTML) {
  const root = ReactDOM.createRoot(rootElement);
  root.render(
    <StrictMode>
      <RouterProvider router={router} />
    </StrictMode>,
  );
}
