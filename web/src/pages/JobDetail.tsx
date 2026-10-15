import { Link, useNavigate, useParams } from "react-router-dom";
import { ApiError } from "../api/client";
import { useJob, useRequeueJob } from "../api/hooks";
import { Query } from "../components/States";
import {
  BlockingBadge,
  CheckPill,
  ConfidenceBadge,
  ImpactBadge,
  JobStatePill,
  TypeBadge,
} from "../components/Badges";
import { absTime, num, shortSha } from "../lib/format";

export function JobDetail() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const q = useJob(id);
  const requeue = useRequeueJob();

  return (
    <>
      <div className="breadcrumb">
        <Link to="/jobs">Pull Requests</Link> / job {id}
      </div>

      <Query {...q} what="job">
        {({ job, findings, check }) => {
          const running = job.state === "running";
          return (
            <>
              <div className="page-head">
                <h1>
                  {job.repo} {job.number ? `#${job.number}` : ""}
                </h1>
                <p>
                  <JobStatePill state={job.state} /> &nbsp;
                  <CheckPill conclusion={check.conclusion} /> &nbsp;
                  <span className="cell-dim">{check.title}</span>
                </p>
              </div>

              <div className="detail-grid">
                <div>
                  <div className="panel">
                    <div className="panel-head">
                      <h2>Findings for this head</h2>
                      <span className="cell-dim">{num(findings.length)}</span>
                    </div>
                    {findings.length === 0 ? (
                      <div className="empty">
                        <div className="empty-title">No findings</div>
                        <div>Nothing was stored for {shortSha(job.headSHA)}.</div>
                      </div>
                    ) : (
                      <table className="grid">
                        <thead>
                          <tr>
                            <th>Claim</th>
                            <th>Verification</th>
                            <th>Impact</th>
                            <th>Location</th>
                          </tr>
                        </thead>
                        <tbody>
                          {findings.map((f) => (
                            <tr
                              key={f.id}
                              onClick={() =>
                                navigate(`/findings/${f.id}?repo=${encodeURIComponent(job.repo)}`)
                              }
                            >
                              <td className="cell-strong">
                                <div className="row-badges">
                                  <TypeBadge value={f.type} />
                                  {f.blocking && <BlockingBadge />}
                                </div>
                                <div style={{ marginTop: 4 }}>{f.claim}</div>
                              </td>
                              <td>
                                <ConfidenceBadge value={f.confidence} />
                              </td>
                              <td>
                                <ImpactBadge value={f.impact} />
                              </td>
                              <td className="cell-mono">
                                {f.path}
                                {f.line ? `:${f.line}` : ""}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    )}
                  </div>
                </div>

                <aside>
                  <div className="panel">
                    <div className="panel-head">
                      <h2>Job</h2>
                    </div>
                    <div className="panel-body">
                      <div className="meta-list">
                        <Meta k="Repo" v={job.repo} />
                        <Meta k="PR number" v={job.number ? `#${job.number}` : "—"} />
                        <Meta k="Head" v={shortSha(job.headSHA)} mono />
                        <Meta k="Base" v={shortSha(job.baseSHA)} mono />
                        <Meta k="Fork" v={job.fork ? "yes" : "no"} />
                        <Meta k="Installation" v={String(job.installationID || "—")} />
                        <Meta k="Action" v={job.action || "—"} />
                        <Meta k="Attempts" v={String(job.attempts)} />
                        <Meta k="Delivery" v={job.deliveryID || "—"} mono />
                        <Meta k="Created" v={absTime(job.createdAt)} />
                        <Meta k="Updated" v={absTime(job.updatedAt)} />
                      </div>
                    </div>
                  </div>

                  <div className="panel" style={{ marginTop: 20 }}>
                    <div className="panel-head">
                      <h2>Re-review</h2>
                    </div>
                    <div className="panel-body">
                      <p className="help" style={{ marginTop: 0 }}>
                        Reset this job to <code>queued</code> so the worker reviews the head again
                        on its next drain.
                      </p>
                      <button
                        className="btn"
                        disabled={running || requeue.isPending}
                        onClick={() => requeue.mutate(job.id)}
                        style={{ width: "100%" }}
                      >
                        {requeue.isPending ? "Re-queuing…" : "Re-queue"}
                      </button>
                      {running && <div className="help">Can't re-queue a running job.</div>}
                      {requeue.isError && (
                        <div className="help" style={{ color: "var(--exploitable)" }}>
                          {requeue.error instanceof ApiError && requeue.error.status === 409
                            ? "Job is running — try again once it finishes."
                            : String((requeue.error as Error)?.message ?? requeue.error)}
                        </div>
                      )}
                      {requeue.isSuccess && (
                        <div className="help" style={{ color: "var(--reproduced)" }}>
                          Re-queued. The worker will pick it up shortly.
                        </div>
                      )}
                    </div>
                  </div>
                </aside>
              </div>
            </>
          );
        }}
      </Query>
    </>
  );
}

function Meta({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="meta-row">
      <span className="k">{k}</span>
      <span className="v" style={mono ? { fontFamily: "var(--mono)", fontSize: 12 } : undefined}>
        {v}
      </span>
    </div>
  );
}
