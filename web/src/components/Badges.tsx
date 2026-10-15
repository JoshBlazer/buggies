import type { Confidence, FindingType, Impact, JobState } from "../api/types";
import { label } from "../lib/format";

export function ConfidenceBadge({ value }: { value: Confidence }) {
  return (
    <span className={`badge badge-${value}`} title="Verification status — ranks above severity">
      <span className="dot" />
      {label(value)}
    </span>
  );
}

export function ImpactBadge({ value }: { value: Impact }) {
  return <span className={`badge badge-${value}`}>{label(value)}</span>;
}

export function TypeBadge({ value }: { value: FindingType }) {
  return <span className="badge badge-type">{value}</span>;
}

export function BlockingBadge() {
  return <span className="badge badge-blocking" title="reproduced × (data-loss or exploitable)">blocking</span>;
}

export function StateBadge({ value }: { value: string }) {
  return <span className="badge badge-neutral">{label(value)}</span>;
}

const JOB_PILL: Record<JobState, string> = {
  queued: "pill-neutral",
  running: "pill-running",
  done: "pill-success",
  failed: "pill-failure",
};

export function JobStatePill({ state }: { state: JobState }) {
  return <span className={`pill ${JOB_PILL[state] ?? "pill-neutral"}`}>{state}</span>;
}

const CHECK_PILL: Record<string, string> = {
  success: "pill-success",
  failure: "pill-failure",
  neutral: "pill-neutral",
};

export function CheckPill({ conclusion }: { conclusion: string }) {
  return <span className={`pill ${CHECK_PILL[conclusion] ?? "pill-neutral"}`}>{conclusion}</span>;
}
