import { useBudget } from "../api/hooks";
import { Query } from "../components/States";
import { Meter } from "../components/Meter";
import { money, num } from "../lib/format";

export function Budget() {
  const q = useBudget();

  return (
    <>
      <div className="page-head">
        <h1>Budget</h1>
        <p>
          The monthly spend cap applies per repo. A repo over its cap is blocked from new reviews
          until the next period or until the cap is raised in Settings.
        </p>
      </div>

      <Query {...q} what="budget">
        {(b) => {
          const total = b.repos.reduce((a, r) => a + r.spentCents, 0);
          return (
            <>
              <div className="callout">
                <strong>{b.month}</strong> · monthly cap{" "}
                <strong>{b.budgetCents > 0 ? money(b.budgetCents) : "none"}</strong> per repo ·{" "}
                {money(total)} spent across {num(b.repos.length)} repos.
              </div>

              {b.repos.length === 0 ? (
                <div className="panel">
                  <div className="empty">
                    <div className="empty-title">No spend yet</div>
                    <div>No repo has consumed budget this period.</div>
                  </div>
                </div>
              ) : (
                <div className="panel">
                  <table className="grid">
                    <thead>
                      <tr>
                        <th>Repo</th>
                        <th style={{ width: "34%" }}>Usage</th>
                        <th className="cell-num">Spent</th>
                        <th>Status</th>
                        <th>Note</th>
                      </tr>
                    </thead>
                    <tbody>
                      {b.repos.map((r) => (
                        <tr key={r.repo} style={{ cursor: "default" }}>
                          <td className="cell-strong">{r.repo}</td>
                          <td>
                            <Meter value={r.spentCents} limit={b.budgetCents} />
                          </td>
                          <td className="cell-num">{money(r.spentCents)}</td>
                          <td>
                            <span className={`pill ${r.allowed ? "pill-success" : "pill-failure"}`}>
                              {r.allowed ? "allowed" : "blocked"}
                            </span>
                          </td>
                          <td className="cell-dim">{r.note || "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          );
        }}
      </Query>
    </>
  );
}
