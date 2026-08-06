import { createHashRouter, RouterProvider, Navigate } from "react-router-dom";
import { AppRoot } from "@/components/layout/AppRoot";
import { Delivery } from "@/features/delivery/Delivery";
import { Daily } from "@/features/daily/Daily";
import { Audit } from "@/features/audit/Audit";
import { Knowledge } from "@/features/knowledge/Knowledge";
import { Terminal } from "@/features/terminal/Terminal";
import { Workspace } from "@/features/workspace/Workspace";
import { Settings } from "@/features/settings/Settings";

const router = createHashRouter([
  {
    element: <AppRoot />,
    children: [
      { index: true, element: <Navigate to="/delivery" replace /> },
      { path: "delivery", element: <Delivery /> },
      { path: "daily", element: <Daily /> },
      { path: "terminal", element: <Terminal /> },
      { path: "audit", element: <Audit /> },
      { path: "knowledge", element: <Knowledge /> },
      { path: "settings", element: <Settings /> },
      { path: "workspace", element: <Workspace /> },
      { path: "*", element: <Navigate to="/delivery" replace /> },
    ],
  },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
