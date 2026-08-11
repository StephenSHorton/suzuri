import { createFileRoute } from "@tanstack/react-router";

// Home chrome + terminal hole are owned by AppShell (Kussetsu root).
// This route exists so the router has a `/` leaf.
export const Route = createFileRoute("/")({
  component: () => null,
});
