// API contract for the Buggies admin dashboard.
//
// These types are the exact shape of the JSON the `buggies serve` daemon serves
// under /api on its loopback admin mux. They mirror the endpoint spec the Go
// side implements; the daemon marshals DTOs to this camelCase shape (not the
// kernel's PascalCase finding.Finding directly) and uses the kernel's *string*
// enum spellings (finding.Confidence.String(), finding.Impact.String()) so the
// wire format is self-describing.
//
// Ranking rule, enforced on every finding surface (VISION.md): verification
// status first, severity second. A reproduced medium outranks an unverified
// critical.

/** Risk class. */
export type FindingType = "correctness" | "contract" | "resilience" | "security";

/** Verification axis — ranks ABOVE impact on every surface. */
export type Confidence = "unverified" | "statically-proven" | "reproduced";

/** Severity axis. */
export type Impact = "cosmetic" | "degraded" | "data-loss" | "exploitable";

/** Lifecycle position — the finding.State machine. */
export type FindingState =
  | "predicted"
  | "verified"
  | "shipped-anyway"
  | "manifested"
  | "attributed"
  | "fixed"
  | "closed"
  | "dismissed";

/** Worker queue state for a reviewed PR head (queue.db jobs.state). */
export type JobState = "queued" | "running" | "done" | "failed";

export type CheckConclusion = "success" | "failure" | "neutral";

// ---- /api/overview ----

export interface QueueDepth {
  queued: number;
  running: number;
  done: number;
  failed: number;
}

export interface Overview {
  queue: QueueDepth;
  findings: {
    total: number;
    blocking: number;
    byState: Record<string, number>;
    byConfidence: Record<string, number>;
  };
  repos: number;
  month: string; // "YYYY-MM"
  spendCents: number;
  budgetCents: number;
  generatedAt: string; // RFC3339
}

// ---- /api/jobs ----

/** A reviewed PR head: a queue.db row joined with its decoded github.PRJob. */
export interface Job {
  id: number;
  key: string;
  repo: string; // owner/repo
  number: number; // PR number
  headSHA: string;
  baseSHA: string;
  fork: boolean;
  installationID: number;
  action: string;
  deliveryID: string;
  state: JobState;
  attempts: number;
  createdAt: string;
  updatedAt: string;
}

export interface JobsPage {
  jobs: Job[];
  total: number;
}

/** A finding as it appears in a list (the /api/findings row shape). */
export interface FindingSummary {
  id: string;
  type: FindingType;
  confidence: Confidence;
  impact: Impact;
  state: FindingState;
  claim: string;
  blocking: boolean;
  path: string;
  symbol: string;
  line: number;
  reproduced: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface JobDetail {
  job: Job;
  findings: FindingSummary[];
  check: { conclusion: CheckConclusion; title: string };
}

// ---- /api/repos ----

export interface RepoRow {
  repo: string; // owner/repo
  installationID: number;
  jobs: QueueDepth;
  lastHeadSHA: string;
  lastReviewedAt: string;
  findings: number;
  blocking: number;
  spendCents: number;
}

export interface ReposResponse {
  repos: RepoRow[];
}

// ---- /api/findings ----

export interface FindingsResponse {
  repo: string;
  findings: FindingSummary[];
}

// ---- /api/findings/{id} ----

export interface SandboxRun {
  stdout: string;
  stderr: string;
  stackTrace: string;
  exitCode: number;
  timingMs: number;
  image: string;
  peakMemoryMB: number;
  cpuSeconds: number;
}

export interface Proof {
  test: { path: string; content: string; cmd: string[] };
  result: {
    outcome: string;
    reproducible: boolean;
    runs: SandboxRun[];
  };
  suggestedFix: string;
}

export interface Location {
  repo: string;
  path: string;
  symbol: string;
  line: number;
}

export interface FindingDetail {
  id: string;
  type: FindingType;
  confidence: Confidence;
  impact: Impact;
  state: FindingState;
  blocking: boolean;
  claim: string;
  location: Location;
  kernelVersion: string;
  createdAt: string;
  updatedAt: string;
  proof: Proof | null;
  renderedMarkdown: string;
}

// ---- /api/findings/{id}/history ----

export interface Transition {
  from: string;
  to: string;
  reason: string;
  at: string;
}

export interface FindingHistory {
  id: string;
  transitions: Transition[];
}

// POST /api/findings/{id}/transition response
export interface TransitionResult {
  id: string;
  state: FindingState;
  updatedAt: string;
}

// ---- /api/budget ----

export interface BudgetRepo {
  repo: string;
  spentCents: number;
  allowed: boolean;
  note: string;
}

export interface Budget {
  month: string;
  budgetCents: number;
  repos: BudgetRepo[];
}

// ---- /api/settings ----

export interface Settings {
  editable: { budgetCents: number };
  readOnly: {
    addr: string;
    intervalSeconds: number;
    githubHost: string;
    githubAppID: number;
    skillsEnabled: boolean;
    allowRunc: boolean;
    dataRoot: string;
    kernelVersion: string;
  };
  hasWebhookSecret: boolean;
  hasAppKey: boolean;
  hasStoreKey: boolean;
}
