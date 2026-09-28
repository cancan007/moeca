import { createHashRouter, RouterProvider, Navigate } from "react-router-dom";
import { AppRoot } from "@/components/layout/AppRoot";
import { Delivery } from "@/features/delivery/Delivery";
import { Daily } from "@/features/daily/Daily";
import { Chat } from "@/features/chat/Chat";
import { Audit } from "@/features/audit/Audit";
import { Knowledge } from "@/features/knowledge/Knowledge";
import { Terminal } from "@/features/terminal/Terminal";
import { Workspace } from "@/features/workspace/Workspace";
import { Settings } from "@/features/settings/Settings";

const router = createHashRouter([
  {
    element: <AppRoot />,
    children: [
      // Chat is where the app opens: it is the screen that needs no setup — no
      // repository, no schedule, no task — so it is the one that has something
      // to show on a fresh install.
      { index: true, element: <Navigate to="/chat" replace /> },
      { path: "chat", element: <Chat /> },
      { path: "daily", element: <Daily /> },
      { path: "delivery", element: <Delivery /> },
      { path: "terminal", element: <Terminal /> },
      { path: "audit", element: <Audit /> },
      { path: "knowledge", element: <Knowledge /> },
      { path: "settings", element: <Settings /> },
      { path: "workspace", element: <Workspace /> },
      { path: "*", element: <Navigate to="/chat" replace /> },
    ],
  },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
