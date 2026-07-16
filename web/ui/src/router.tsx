import { createBrowserRouter, Navigate, Outlet } from "react-router";
import { useSession } from "@/lib/session";
import Placeholder from "@/pages/Placeholder";

function RequireAuth() {
  const { data: session, isLoading } = useSession();
  if (isLoading) return <div className="p-8 text-muted-2">Loading…</div>;
  if (!session?.authenticated) return <Navigate to="/login" replace />;
  if (session.owner?.must_change_password) return <Navigate to="/change-password" replace />;
  if (!session.workspace) return <Navigate to="/workspaces" replace />;
  return <Outlet />;
}

// Tasks 5-7 replace these placeholder elements with real screens.
export const router = createBrowserRouter(
  [
    { path: "/login", element: <Placeholder title="Login" /> },
    { path: "/change-password", element: <Placeholder title="Change password" /> },
    { path: "/workspaces", element: <Placeholder title="Workspaces" /> },
    {
      element: <RequireAuth />,
      children: [
        {
          // AppShell mounts here in Task 6.
          element: <Outlet />,
          children: [
            { path: "/", element: <Placeholder title="Home" /> },
            { path: "/kb", element: <Placeholder title="Knowledge Base" /> },
            { path: "/agents", element: <Placeholder title="Agents" /> },
            { path: "/skills", element: <Placeholder title="Skills" /> },
            { path: "/connections", element: <Placeholder title="Connections" /> },
            { path: "/chats", element: <Placeholder title="Chats" /> },
            { path: "/secrets", element: <Placeholder title="Secrets" /> },
            { path: "/settings", element: <Placeholder title="Settings" /> },
          ],
        },
      ],
    },
  ],
  { basename: "/app" },
);
