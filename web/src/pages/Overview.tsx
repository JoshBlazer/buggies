import { Link } from "react-router-dom";
import { useOverview } from "../api/hooks";
import { Query } from "../components/States";
import { StatCard } from "../components/StatCard";
import { Meter } from "../components/Meter";
import { money, num, label, absTime } from "../lib/format";

export function Overview() {
  const q = useOverview();

  return (
    <>
      <div className="page-head">
        <h1>Overview</h1>
        <p>
          Buggies catches the bug before it ships and proves it did after. Verification status
          ranks above severity on every surface here.
        </p>
      </div>

      <Query {...q} what="overview">
        {(o) => (
          <>
            <div className="stat-grid">
              <StatCard
                label="Blocking findings"
                value={num(o.findings.blocking)}
                tone={o.findings.blocking > 0 ? "alert" : "good"}
                sub="reproduced × (data-loss or exploitable)"
              />
              <StatCard label="Open findings" value={num(o.findings.total)} sub="all states" />
              <StatCard label="Repos seen" value={num(o.repos)} sub="with a review on disk" />
              <StatCard
                label="Running / queued"
                value={`${num(o.queue.running)} / ${num(o.queue.queued)}`}
                sub="worker drain"
              />
              <StatCard
                label={`Spend · ${o.month}`}
                value={money(o.spendCents)}
                sub={o.budgetCents > 0 ? `of ${money(o.budgetCents)} cap` : "no cap set"}
              />
              <StatCard
                label="Jobs done / failed"
                value={`${num(o.queue.done)} / ${num(o.queue.failed)}`}
                tone={o.queue.failed > 0 ? "alert" : undefined}
                sub="lifetime queue"
              />
            </div>

            {o.budgetCents > 0 && (
              <div className="panel" style={{ marginBottom: 22 }}>
                <div className="panel-head">
                  <h2>Monthly budget</h2>
                  <span className="cell-dim">
                    {money(o.spendCents)} / {money(o.budgetCents)}
                  </span>
                </div>
                <div className="panel-body">
                  <Meter value={o.spendCents} limit={o.budgetCents} />
                </div>
              </div>
            )}

            <div className="grid-2">
              <Breakdown
                title="Findings by confidence"
                counts={o.findings.byConfidence}
                order={["reproduced", "statically-proven", "unverified"]}
              />
              <Breakdown
                title="Findings by state"
                counts={o.findings.byState}
                order={[
                  "verified",
                  "predicted",
                  "shipped-anyway",
                  "manifested",
                  "attributed",
                  "fixed",
                  "closed",
                  "dismissed",
                ]}
              />
            </div>

            <p className="help" style={{ marginTop: 18 }}>
              Generated {absTime(o.generatedAt)}. <Link to="/findings">Open findings →</Link>
            </p>
          </>
        )}
      </Query>
    </>
  );
}

function Breakdown({
  title,
  counts,
  order,
}: {
  title: string;
  counts: Record<string, number>;
  order: string[];
}) {
  const keys = Object.keys(counts ?? {});
  const sorted = [
    ...order.filter((k) => keys.includes(k)),
    ...keys.filter((k) => !order.includes(k)),
  ];
  const total = Object.values(counts ?? {}).reduce((a, b) => a + b, 0);

  return (
    <div className="panel">
      <div className="panel-head">
        <h2>{title}</h2>
        <span className="cell-dim">{num(total)}</span>
      </div>
      <div className="panel-body">
        {sorted.length === 0 ? (
          <div className="cell-dim">No findings yet.</div>
        ) : (
          <div className="meta-list">
            {sorted.map((k) => (
              <div className="meta-row" key={k}>
                <span className="k">{label(k)}</span>
                <span className="v" style={{ fontVariantNumeric: "tabular-nums" }}>
                  {num(counts[k])}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
