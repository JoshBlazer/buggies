import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useJobs } from "../api/hooks";
import type { JobState } from "../api/types";
import { Query } from "../components/States";
import { JobStatePill } from "../components/Badges";
import { num, relativeTime, shortSha } from "../lib/format";

const STATES: (JobState | "")[] = ["", "queued", "running", "done", "failed"];
const PAGE = 50;

export function PullRequests() {
  const [state, setState] = useState("");
  const [repo, setRepo] = useState("");
  const [offset, setOffset] = useState(0);
  const navigate = useNavigate();

  const q = useJobs({ state: state || undefined, repo: repo || undefined, limit: PAGE, offset });

  return (
    <>
      <div className="page-head">
        <h1>Pull Requests</h1>
        <p>
          Every PR head the daemon has queued or reviewed, newest first. Each row is a queue job
          joined to its decoded webhook payload.
        </p>
      </div>

      <div className="toolbar">
        <div className="field">
          <label>State</label>
          <select
            value={state}
            onChange={(e) => {
              setState(e.target.value);
              setOffset(0);
            }}
          >
            {STATES.map((s) => (
              <option key={s} value={s}>
                {s || "all"}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label>Repo</label>
          <input
            type="text"
            value={repo}
            placeholder="owner/repo"
            onChange={(e) => {
              setRepo(e.target.value);
              setOffset(0);
            }}
          />
        </div>
      </div>

      <Query {...q} what="pull requests">
        {(page) =>
          page.jobs.length === 0 ? (
            <div className="panel">
              <div className="empty">
                <div className="empty-title">No matching jobs</div>
                <div>No PR heads match these filters yet.</div>
              </div>
            </div>
          ) : (
            <>
              <div className="panel">
                <table className="grid">
                  <thead>
                    <tr>
                      <th>Repo</th>
                      <th>PR</th>
                      <th>Head</th>
                      <th>State</th>
                      <th className="cell-num">Attempts</th>
                      <th>Action</th>
                      <th>Updated</th>
                    </tr>
                  </thead>
                  <tbody>
                    {page.jobs.map((j) => (
                      <tr key={j.id} onClick={() => navigate(`/jobs/${j.id}`)}>
                        <td className="cell-strong">
                          {j.repo}
                          {j.fork && (
                            <span className="badge badge-neutral" style={{ marginLeft: 8 }}>
                              fork
                            </span>
                          )}
                        </td>
                        <td className="cell-dim">#{j.number || "—"}</td>
                        <td className="cell-mono">{shortSha(j.headSHA)}</td>
                        <td>
                          <JobStatePill state={j.state} />
                        </td>
                        <td className="cell-num">{j.attempts}</td>
                        <td className="cell-dim">{j.action || "—"}</td>
                        <td className="cell-dim">{relativeTime(j.updatedAt)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <div className="toolbar" style={{ marginTop: 16, justifyContent: "space-between" }}>
                <span className="cell-dim">
                  {offset + 1}–{offset + page.jobs.length} of {num(page.total)}
                </span>
                <span style={{ display: "flex", gap: 10 }}>
                  <button
                    className="btn btn-ghost"
                    disabled={offset === 0}
                    onClick={() => setOffset(Math.max(0, offset - PAGE))}
                  >
                    Previous
                  </button>
                  <button
                    className="btn btn-ghost"
                    disabled={offset + page.jobs.length >= page.total}
                    onClick={() => setOffset(offset + PAGE)}
                  >
                    Next
                  </button>
                </span>
              </div>
            </>
          )
        }
      </Query>
    </>
  );
}
