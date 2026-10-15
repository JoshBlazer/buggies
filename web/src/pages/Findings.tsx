import { useEffect } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { useFindings, useRepos } from "../api/hooks";
import type { Confidence } from "../api/types";
import { Query } from "../components/States";
import { BlockingBadge, ConfidenceBadge, ImpactBadge, StateBadge, TypeBadge } from "../components/Badges";
import { confidenceRank, impactRank, num } from "../lib/format";

const TYPES = ["", "correctness", "contract", "resilience", "security"];
const STATES = ["", "predicted", "verified", "shipped-anyway", "manifested", "attributed", "fixed", "closed", "dismissed"];
const CONFIDENCES = ["", "unverified", "statically-proven", "reproduced"];

export function Findings() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const repos = useRepos();

  const repo = params.get("repo") ?? "";
  const type = params.get("type") ?? "";
  const state = params.get("state") ?? "";
  const minConfidence = params.get("minConfidence") ?? "";
  const path = params.get("path") ?? "";

  // Default to the first repo once the repo list loads, if none chosen yet.
  const repoList = repos.data?.repos ?? [];
  useEffect(() => {
    if (!repo && repoList.length > 0) {
      setParam("repo", repoList[0].repo);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [repo, repoList.length]);

  function setParam(key: string, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next, { replace: true });
  }

  const q = useFindings({
    repo,
    type: type || undefined,
    state: state || undefined,
    minConfidence: minConfidence || undefined,
    path: path || undefined,
    limit: 200,
  });

  return (
    <>
      <div className="page-head">
        <h1>Findings</h1>
        <p>
          Findings are stored per repo. Rows are ordered by verification first, then severity — a
          reproduced medium outranks an unverified critical.
        </p>
      </div>

      <div className="toolbar">
        <div className="field">
          <label>Repo</label>
          <select value={repo} onChange={(e) => setParam("repo", e.target.value)}>
            {repoList.length === 0 && <option value="">(no repos)</option>}
            {repoList.map((r) => (
              <option key={r.repo} value={r.repo}>
                {r.repo}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label>Type</label>
          <select value={type} onChange={(e) => setParam("type", e.target.value)}>
            {TYPES.map((t) => (
              <option key={t} value={t}>
                {t || "all"}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label>State</label>
          <select value={state} onChange={(e) => setParam("state", e.target.value)}>
            {STATES.map((s) => (
              <option key={s} value={s}>
                {s || "all"}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label>Min confidence</label>
          <select value={minConfidence} onChange={(e) => setParam("minConfidence", e.target.value)}>
            {CONFIDENCES.map((c) => (
              <option key={c} value={c}>
                {c || "any"}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label>Path contains</label>
          <input
            type="text"
            value={path}
            placeholder="src/…"
            onChange={(e) => setParam("path", e.target.value)}
          />
        </div>
      </div>

      {!repo ? (
        <div className="panel">
          <div className="empty">
            <div className="empty-title">Pick a repo</div>
            <div>Findings are per repo — choose one above to list them.</div>
          </div>
        </div>
      ) : (
        <Query {...q} what="findings">
          {(resp) => {
            const findings = [...resp.findings].sort(
              (a, b) =>
                confidenceRank(b.confidence) - confidenceRank(a.confidence) ||
                impactRank(b.impact) - impactRank(a.impact),
            );
            return findings.length === 0 ? (
              <div className="panel">
                <div className="empty">
                  <div className="empty-title">No findings</div>
                  <div>Nothing matches these filters for {resp.repo}.</div>
                </div>
              </div>
            ) : (
              <div className="panel">
                <div className="panel-head">
                  <h2>{resp.repo}</h2>
                  <span className="cell-dim">{num(findings.length)}</span>
                </div>
                <table className="grid">
                  <thead>
                    <tr>
                      <th>Claim</th>
                      <th>Verification</th>
                      <th>Impact</th>
                      <th>State</th>
                      <th>Location</th>
                    </tr>
                  </thead>
                  <tbody>
                    {findings.map((f) => (
                      <tr
                        key={f.id}
                        onClick={() =>
                          navigate(`/findings/${f.id}?repo=${encodeURIComponent(resp.repo)}`)
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
                          <ConfidenceBadge value={f.confidence as Confidence} />
                        </td>
                        <td>
                          <ImpactBadge value={f.impact} />
                        </td>
                        <td>
                          <StateBadge value={f.state} />
                        </td>
                        <td className="cell-mono">
                          {f.path}
                          {f.line ? `:${f.line}` : ""}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          }}
        </Query>
      )}
    </>
  );
}
