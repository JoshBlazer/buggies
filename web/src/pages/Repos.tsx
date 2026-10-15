import { useNavigate } from "react-router-dom";
import { useRepos } from "../api/hooks";
import { Query } from "../components/States";
import { money, num, relativeTime, shortSha } from "../lib/format";

export function Repos() {
  const q = useRepos();
  const navigate = useNavigate();

  return (
    <>
      <div className="page-head">
        <h1>Repos &amp; installations</h1>
        <p>
          Every repo the daemon has seen, derived from the queue plus the per-repo data dirs on
          disk, with job counts, last review, findings, and this month's spend.
        </p>
      </div>

      <Query {...q} what="repos">
        {(resp) =>
          resp.repos.length === 0 ? (
            <div className="panel">
              <div className="empty">
                <div className="empty-title">No repos yet</div>
                <div>Nothing has been queued for review on this daemon.</div>
              </div>
            </div>
          ) : (
            <div className="panel">
              <table className="grid">
                <thead>
                  <tr>
                    <th>Repo</th>
                    <th>Install</th>
                    <th>Jobs (q/r/d/f)</th>
                    <th>Last head</th>
                    <th>Last review</th>
                    <th className="cell-num">Findings</th>
                    <th className="cell-num">Blocking</th>
                    <th className="cell-num">Spend</th>
                  </tr>
                </thead>
                <tbody>
                  {resp.repos.map((r) => (
                    <tr
                      key={r.repo}
                      onClick={() => navigate(`/findings?repo=${encodeURIComponent(r.repo)}`)}
                    >
                      <td className="cell-strong">{r.repo}</td>
                      <td className="cell-dim">{r.installationID || "—"}</td>
                      <td className="cell-mono">
                        {r.jobs.queued}/{r.jobs.running}/{r.jobs.done}/{r.jobs.failed}
                      </td>
                      <td className="cell-mono">{shortSha(r.lastHeadSHA)}</td>
                      <td className="cell-dim">{relativeTime(r.lastReviewedAt)}</td>
                      <td className="cell-num">{num(r.findings)}</td>
                      <td className="cell-num">
                        {r.blocking > 0 ? (
                          <span style={{ color: "var(--blocking)", fontWeight: 700 }}>
                            {num(r.blocking)}
                          </span>
                        ) : (
                          num(r.blocking)
                        )}
                      </td>
                      <td className="cell-num">{money(r.spendCents)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )
        }
      </Query>
    </>
  );
}
