import { lazy, Suspense, type ComponentType, type ReactNode } from "react";
import { createBrowserRouter, Navigate, Outlet } from "react-router";
import LockScreen from "@/pages/LockScreen";
import { useSession } from "@/lib/session";
import { AppShell } from "@/components/shell/AppShell";
import HomePage from "@/pages/home/HomePage";
import Login from "@/pages/Login";
import ChangePassword from "@/pages/ChangePassword";
import Workspaces from "@/pages/Workspaces";
import ChatsPage from "@/pages/chats/ChatsPage";
import AgentsPage from "@/pages/agents/AgentsPage";
import AgentDetailPage from "@/pages/agents/AgentDetailPage";
import SkillsPage from "@/pages/skills/SkillsPage";
import SkillDetailPage, { CoreSkillViewPage } from "@/pages/skills/SkillDetailPage";
import SecretsPage from "@/pages/secrets/SecretsPage";

// Heavy, off-first-paint surfaces are route-split so the entry chunk stays
// small: the KB page pulls in the whole TipTap editor, the designer
// surfaces pull in the agent/skill design chat UI, and settings/connections/
// setup carry their own large forms. Same loading affordance the auth
// guards below already use, so a lazy route doesn't look different from a
// slow session check.
function RouteFallback() {
  return <div className="p-8 text-muted-2">Loading…</div>;
}

function lazyRoute(loader: () => Promise<{ default: ComponentType }>): ReactNode {
  const LazyComponent = lazy(loader);
  return (
    <Suspense fallback={<RouteFallback />}>
      <LazyComponent />
    </Suspense>
  );
}

function RequireAuth() {
  const { data: session, isLoading } = useSession();
  if (isLoading) return <RouteFallback />;
  if (!session?.authenticated) return <Navigate to="/login" replace />;
  if (session.owner?.must_change_password) return <Navigate to="/change-password" replace />;
  // Checked before the workspace redirect: locking keeps the workspace, so a
  // locked session must land on the lock screen rather than being bounced to
  // the workspace picker. Rendered in place rather than as a route so the
  // current URL survives — unlocking returns you where you were.
  if (session.locked) return <LockScreen />;
  if (!session.workspace) return <Navigate to="/workspaces" replace />;
  if (session.workspace?.needs_setup) return <Navigate to="/setup" replace />;
  return <Outlet />;
}

// Guards the full-screen onboarding wizard: must be an authenticated owner
// with an active workspace that still needs setup — otherwise there's
// nothing for /setup to do, so bounce to "/" (which itself redirects
// appropriately via RequireAuth for every other unmet precondition).
function RequireSetupWorkspace() {
  const { data: session, isLoading } = useSession();
  if (isLoading) return <RouteFallback />;
  if (!session?.authenticated) return <Navigate to="/login" replace />;
  if (session.owner?.must_change_password) return <Navigate to="/change-password" replace />;
  if (!session.workspace?.needs_setup) return <Navigate to="/" replace />;
  return <Outlet />;
}

// Later tasks replace these placeholder elements with real screens.
export const router = createBrowserRouter(
  [
    { path: "/login", element: <Login /> },
    { path: "/change-password", element: <ChangePassword /> },
    { path: "/workspaces", element: <Workspaces /> },
    {
      element: <RequireSetupWorkspace />,
      children: [
        { path: "/setup", element: lazyRoute(() => import("@/pages/setup/SetupWizard")) },
      ],
    },
    {
      element: <RequireAuth />,
      children: [
        {
          element: <AppShell />,
          children: [
            { path: "/", element: <HomePage /> },
            { path: "/kb", element: lazyRoute(() => import("@/pages/kb/KBPage")) },
            { path: "/agents", element: <AgentsPage /> },
            { path: "/agents/new", element: lazyRoute(() => import("@/pages/agents/AgentNewPage")) },
            { path: "/agents/:id", element: <AgentDetailPage /> },
            {
              path: "/agents/:id/edit",
              element: lazyRoute(() => import("@/pages/agents/AgentEditPage")),
            },
            { path: "/skills", element: <SkillsPage /> },
            { path: "/skills/new", element: lazyRoute(() => import("@/pages/skills/SkillNewPage")) },
            { path: "/skills/core/:slug", element: <CoreSkillViewPage /> },
            { path: "/skills/:id", element: <SkillDetailPage /> },
            {
              path: "/connections",
              element: lazyRoute(() => import("@/pages/connections/ConnectionsPage")),
            },
            { path: "/chats", element: <ChatsPage /> },
            { path: "/secrets", element: <SecretsPage /> },
            { path: "/settings", element: lazyRoute(() => import("@/pages/settings/SettingsPage")) },
          ],
        },
      ],
    },
  ],
  { basename: "/" },
);
