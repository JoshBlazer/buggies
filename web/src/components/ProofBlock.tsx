import type { FindingDetail } from "../api/types";

// ProofBlock is the on-screen equivalent of surfaces/prcomment.Render: the
// failing test, its sandbox output, and the suggested fix diff. This is the
// whole product promise made visible — the developer sees a test that fails,
// not a sentence that might be true.
export function ProofBlock({ finding }: { finding: FindingDetail }) {
  if (finding.confidence !== "reproduced" || !finding.proof) {
    return (
      <div className="note-static">
        Static only — not yet reproduced. Treat this as a lead, not a fact. It is held back from
        the developer surfaces until the sandbox reproduces it.
      </div>
    );
  }

  const p = finding.proof;
  const runs = p.result?.runs ?? [];
  const n = runs.length;

  return (
    <div className="proof">
      <div className="proof-head">
        <span className="dot" />
        Failing test — {p.result?.reproducible ? "reproduced" : "ran"} {n}
        {n === 1 ? " run" : " runs"} in the sandbox
        {p.result?.outcome ? ` · ${p.result.outcome}` : ""}
      </div>

      <div className="proof-section">
        <div className="proof-section-label">{p.test?.path || "generated test"}</div>
        <pre className="code">
          <code>{(p.test?.content ?? "").replace(/\n+$/, "")}</code>
        </pre>
        {p.test?.cmd?.length ? (
          <div className="proof-section-label" style={{ paddingBottom: 10 }}>
            cmd: <code>{p.test.cmd.join(" ")}</code>
          </div>
        ) : null}
      </div>

      {runs.map((run, i) => {
        const out = [run.stdout, run.stderr, run.stackTrace].filter((s) => s && s.trim()).join("\n");
        return (
          <div className="proof-section" key={i}>
            <div className="proof-section-label">
              Run {i + 1} · exit {run.exitCode} · {run.timingMs}ms
              {run.image ? ` · ${run.image}` : ""}
              {run.peakMemoryMB ? ` · ${run.peakMemoryMB}MB peak` : ""}
              {run.cpuSeconds ? ` · ${run.cpuSeconds}s cpu` : ""}
            </div>
            {out.trim() ? (
              <pre className="code">
                <code>{out.trim()}</code>
              </pre>
            ) : (
              <div className="proof-section-label" style={{ paddingBottom: 10 }}>
                (no captured output)
              </div>
            )}
          </div>
        );
      })}

      {p.suggestedFix?.trim() && (
        <div className="proof-section">
          <div className="proof-section-label">Suggested fix</div>
          <Diff text={p.suggestedFix} />
        </div>
      )}
    </div>
  );
}

function Diff({ text }: { text: string }) {
  const lines = text.replace(/\n+$/, "").split("\n");
  return (
    <pre className="code diff">
      <code>
        {lines.map((line, i) => {
          let cls = "";
          if (line.startsWith("+")) cls = "add";
          else if (line.startsWith("-")) cls = "del";
          else if (line.startsWith("@@")) cls = "hunk";
          return (
            <span key={i} className={cls}>
              {line}
              {"\n"}
            </span>
          );
        })}
      </code>
    </pre>
  );
}
