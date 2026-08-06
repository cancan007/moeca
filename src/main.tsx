import React from "react";
import ReactDOM from "react-dom/client";
// Must come before App: the store seeds its agent-template defaults from the
// active locale at module-eval time, and modules evaluate in import order.
// (lib/templates.ts also imports i18n directly, so the ordering holds even if
// this line moves — this is the statement of intent, not the only guard.)
import "@/i18n";
import App from "./App";
import "./styles/index.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
