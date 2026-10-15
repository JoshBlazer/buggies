import { NavLink, Outlet } from "react-router-dom";
import { useOverview } from "../api/hooks";
import { clearToken } from "../api/auth";

const NAV = [
  { to: "/", label: "Overview", end: true },
  { to: "/jobs", label: "Pull Requests", end: false },
  { to: "/findings", label: "Findings", end: false },
  { to: "/repos", label: "Repos", end: false },
  { to: "/budget", label: "Budget", end: false },
  { to: "/settings", label: "Settings", end: false },
];

export function Layout() {
  const { data } = useOverview();

  const running = data?.queue.running ?? 0;
  const queued = data?.queue.queued ?? 0;
  const counts: Record<string, { n: number; alert?: boolean }> = {
    "/jobs": { n: queued + running },
    "/findings": {
      n: data?.findings.total ?? 0,
      alert: (data?.findings.blocking ?? 0) > 0,
    },
    "/repos": { n: data?.repos ?? 0 },
  };

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">Buggies</span>
          <span className="brand-tag">proof, not opinion</span>
        </div>
        {NAV.map((item) => {
          const c = counts[item.to];
          return (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}
            >
              {item.label}
              {c && c.n > 0 && (
                <span className={`nav-count${c.alert ? " alert" : ""}`}>{c.n}</span>
              )}
            </NavLink>
          );
        })}
        <div className="sidebar-foot">
          <div style={{ marginBottom: 10 }}>
            Buggies reviews every PR in a sandbox and ships only what it can reproduce. A green
            check that flips red on a re-run never happens here.
          </div>
          <button className="btn btn-ghost" style={{ width: "100%" }} onClick={() => clearToken()}>
            Lock console
          </button>
        </div>
      </aside>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
