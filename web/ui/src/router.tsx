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
import AgentNewPage from "@/pages/agents/AgentNewPage";
import AgentDetailPage from "@/pages/agents/AgentDetailPage";
import AgentEditPage from "@/pages/agents/AgentEditPage";
import SkillsPage from "@/pages/skills/SkillsPage";
import SkillNewPage from "@/pages/skills/SkillNewPage";
import SkillDetailPage, { CoreSkillViewPage } from "@/pages/skills/SkillDetailPage";
import ConnectionsPage from "@/pages/connections/ConnectionsPage";
import SettingsPage from "@/pages/settings/SettingsPage";

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
            { path: "/agents/new", element: <AgentNewPage /> },
            { path: "/agents/:id", element: <AgentDetailPage /> },
            { path: "/agents/:id/edit", element: <AgentEditPage /> },
            { path: "/skills", element: <SkillsPage /> },
            { path: "/skills/new", element: <SkillNewPage /> },
            { path: "/skills/core/:slug", element: <CoreSkillViewPage /> },
            { path: "/skills/:id", element: <SkillDetailPage /> },
            { path: "/connections", element: <ConnectionsPage /> },
            { path: "/chats", element: <ChatsPage /> },
            { path: "/secrets", element: <Placeholder title="Secrets" /> },
            { path: "/settings", element: <SettingsPage /> },
          ],
        },
      ],
    },
  ],
  { basename: "/app" },
);
