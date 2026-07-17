import { createBrowserRouter, Navigate, Outlet } from "react-router";
import { useSession } from "@/lib/session";
import { AppShell } from "@/components/shell/AppShell";
import Placeholder from "@/pages/Placeholder";
import Login from "@/pages/Login";
import ChangePassword from "@/pages/ChangePassword";
import Workspaces from "@/pages/Workspaces";
import KBPage from "@/pages/kb/KBPage";
import ChatsPage from "@/pages/chats/ChatsPage";
import AgentsPage from "@/pages/agents/AgentsPage";

function RequireAuth() {
  const { data: session, isLoading } = useSession();
  if (isLoading) return <div className="p-8 text-muted-2">Loading…</div>;
  if (!session?.authenticated) return <Navigate to="/login" replace />;
  if (session.owner?.must_change_password) return <Navigate to="/change-password" replace />;
  if (!session.workspace) return <Navigate to="/workspaces" replace />;
  if (session.workspace?.needs_setup)
    return <Navigate to={`/workspaces?setup=${session.workspace.id}`} replace />;
  return <Outlet />;
}

// Later tasks replace these placeholder elements with real screens.
export const router = createBrowserRouter(
  [
    { path: "/login", element: <Login /> },
    { path: "/change-password", element: <ChangePassword /> },
    { path: "/workspaces", element: <Workspaces /> },
    {
      element: <RequireAuth />,
      children: [
        {
          element: <AppShell />,
          children: [
            { path: "/", element: <Placeholder title="Home" /> },
            { path: "/kb", element: <KBPage /> },
            { path: "/agents", element: <AgentsPage /> },
            { path: "/agents/new", element: <Placeholder title="New agent" /> },
            { path: "/agents/:id", element: <Placeholder title="Agent" /> },
            { path: "/agents/:id/edit", element: <Placeholder title="Edit agent" /> },
            { path: "/skills", element: <Placeholder title="Skills" /> },
            { path: "/connections", element: <Placeholder title="Connections" /> },
            { path: "/chats", element: <ChatsPage /> },
            { path: "/secrets", element: <Placeholder title="Secrets" /> },
            { path: "/settings", element: <Placeholder title="Settings" /> },
          ],
        },
      ],
    },
  ],
  { basename: "/app" },
);
