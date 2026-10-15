import { createHashRouter, RouterProvider } from "react-router-dom";
import { Layout } from "./components/Layout";
import { Overview } from "./pages/Overview";
import { PullRequests } from "./pages/PullRequests";
import { JobDetail } from "./pages/JobDetail";
import { Findings } from "./pages/Findings";
import { FindingDetail } from "./pages/FindingDetail";
import { Repos } from "./pages/Repos";
import { Budget } from "./pages/Budget";
import { Settings } from "./pages/Settings";
import { Empty } from "./components/States";

// HashRouter rather than BrowserRouter: the dashboard ships as a flat bundle of
// static assets served by the Go daemon, which has no SPA deep-link fallback.
// Hash routing keeps every route resolvable from index.html without any
// server-side rewrite, which is what "buildable as static assets" demands.
const router = createHashRouter([
  {
    path: "/",
    element: <Layout />,
    children: [
      { index: true, element: <Overview /> },
      { path: "jobs", element: <PullRequests /> },
      { path: "jobs/:id", element: <JobDetail /> },
      { path: "findings", element: <Findings /> },
      { path: "findings/:id", element: <FindingDetail /> },
      { path: "repos", element: <Repos /> },
      { path: "budget", element: <Budget /> },
      { path: "settings", element: <Settings /> },
      { path: "*", element: <Empty title="Not found" /> },
    ],
  },
]);

export function App() {
  return <RouterProvider router={router} />;
}
