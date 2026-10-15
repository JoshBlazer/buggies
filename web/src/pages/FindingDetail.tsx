import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useFinding, useFindingHistory, useTransitionFinding } from "../api/hooks";
import type { FindingState } from "../api/types";
import { Query } from "../components/States";
import { ProofBlock } from "../components/ProofBlock";
import { BlockingBadge, ConfidenceBadge, ImpactBadge, StateBadge, TypeBadge } from "../components/Badges";
import { absTime, label } from "../lib/format";

// The transitions a human can drive from the console. The daemon is the final
// authority on which are legal from the current state; it rejects the rest.
const TARGETS: FindingState[] = [
  "verified",
  "dismissed",
  "fixed",
  "closed",
  "shipped-anyway",
];

export function FindingDetail() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const repo = params.get("repo") ?? "";

  const q = useFinding(repo, id);
  const history = useFindingHistory(repo, id);

  if (!repo) {
    return (
      <>
        <div className="breadcrumb">
          <Link to="/findings">Findings</Link> / {id}
        </div>
        <div className="panel">
          <div className="empty">
            <div className="empty-title">Missing repo</div>
            <div>
              This finding can only be opened with its repo. <Link to="/findings">Back to findings</Link>.
            </div>
          </div>
        </div>
      </>
    );
  }

  return (
    <>
      <div className="breadcrumb">
        <Link to={`/findings?repo=${encodeURIComponent(repo)}`}>Findings</Link> / {repo} / {id.slice(0, 12)}
      </div>

      <Query {...q} what="finding">
        {(f) => (
          <>
            <div className="page-head">
              <div className="row-badges" style={{ marginBottom: 8 }}>
                <TypeBadge value={f.type} />
                <ConfidenceBadge value={f.confidence} />
                <ImpactBadge value={f.impact} />
                <StateBadge value={f.state} />
                {f.blocking && <BlockingBadge />}
              </div>
              <h1 className="claim">{f.claim}</h1>
              <div className="loc">
                {f.location.path}
                {f.location.line ? `:${f.location.line}` : ""}
                {f.location.symbol ? ` · ${f.location.symbol}` : ""}
              </div>
            </div>

            <div className="detail-grid">
              <div>
                <ProofBlock finding={f} />

                {f.renderedMarkdown?.trim() && (
                  <div className="panel" style={{ marginTop: 20 }}>
                    <div className="panel-head">
                      <h2>Rendered PR comment</h2>
                    </div>
                    <pre className="code">
                      <code>{f.renderedMarkdown.trim()}</code>
                    </pre>
                  </div>
                )}

                <div className="panel" style={{ marginTop: 20 }}>
                  <div className="panel-head">
                    <h2>Audit trail</h2>
                  </div>
                  {history.isLoading ? (
                    <div className="panel-body cell-dim">Loading history…</div>
                  ) : (history.data?.transitions.length ?? 0) === 0 ? (
                    <div className="panel-body cell-dim">No transitions recorded.</div>
                  ) : (
                    <table className="grid">
                      <thead>
                        <tr>
                          <th>From</th>
                          <th>To</th>
                          <th>Reason</th>
                          <th>At</th>
                        </tr>
                      </thead>
                      <tbody>
                        {history.data!.transitions.map((t, i) => (
                          <tr key={i} style={{ cursor: "default" }}>
                            <td className="cell-dim">{label(t.from) || "—"}</td>
                            <td className="cell-strong">{label(t.to)}</td>
                            <td className="cell-dim">{t.reason || "—"}</td>
                            <td className="cell-dim">{absTime(t.at)}</td>
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
                    <h2>Details</h2>
                  </div>
                  <div className="panel-body">
                    <div className="meta-list">
                      <Meta k="ID" v={f.id} mono />
                      <Meta k="Type" v={label(f.type)} />
                      <Meta k="Confidence" v={label(f.confidence)} />
                      <Meta k="Impact" v={label(f.impact)} />
                      <Meta k="State" v={label(f.state)} />
                      <Meta k="Repo" v={f.location.repo} />
                      <Meta k="Kernel" v={f.kernelVersion || "—"} />
                      <Meta k="Created" v={absTime(f.createdAt)} />
                      <Meta k="Updated" v={absTime(f.updatedAt)} />
                    </div>
                  </div>
                </div>

                <TransitionForm id={f.id} repo={repo} current={f.state} />
              </aside>
            </div>
          </>
        )}
      </Query>
    </>
  );
}

function TransitionForm({ id, repo, current }: { id: string; repo: string; current: FindingState }) {
  const [to, setTo] = useState<FindingState>(
    TARGETS.find((t) => t !== current) ?? "dismissed",
  );
  const [reason, setReason] = useState("");
  const transition = useTransitionFinding();

  function submit(e: React.FormEvent) {
    e.preventDefault();
    transition.mutate({ id, repo, to, reason: reason.trim() });
  }

  return (
    <div className="panel" style={{ marginTop: 20 }}>
      <div className="panel-head">
        <h2>Change state</h2>
      </div>
      <form className="panel-body" onSubmit={submit}>
        <div className="field" style={{ marginBottom: 12 }}>
          <label>New state</label>
          <select value={to} onChange={(e) => setTo(e.target.value as FindingState)}>
            {TARGETS.map((t) => (
              <option key={t} value={t}>
                {label(t)}
              </option>
            ))}
          </select>
        </div>
        <div className="field" style={{ marginBottom: 12 }}>
          <label>Reason</label>
          <input
            type="text"
            value={reason}
            placeholder="Recorded in the audit trail"
            onChange={(e) => setReason(e.target.value)}
          />
        </div>
        <button className="btn" type="submit" disabled={transition.isPending} style={{ width: "100%" }}>
          {transition.isPending ? "Applying…" : `Mark ${label(to)}`}
        </button>
        {transition.isError && (
          <div className="help" style={{ color: "var(--exploitable)" }}>
            {String((transition.error as Error)?.message ?? transition.error)}
          </div>
        )}
        {transition.isSuccess && (
          <div className="help" style={{ color: "var(--reproduced)" }}>
            Updated to {label(transition.data.state)}.
          </div>
        )}
      </form>
    </div>
  );
}

function Meta({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="meta-row">
      <span className="k">{k}</span>
      <span
        className="v"
        style={mono ? { fontFamily: "var(--mono)", fontSize: 12, wordBreak: "break-all" } : undefined}
      >
        {v}
      </span>
    </div>
  );
}
