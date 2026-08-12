import { createFileRoute } from "@tanstack/react-router";

// Settings chrome is owned by AppShell (Kussetsu scene switch).
export const Route = createFileRoute("/settings")({
  component: () => null,
});
