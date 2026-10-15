import type { Confidence, Impact } from "../api/types";

export function money(cents: number): string {
  return new Intl.NumberFormat(undefined, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: 2,
  }).format((cents ?? 0) / 100);
}

export function pct(ratio: number): string {
  return `${Math.round((ratio ?? 0) * 100)}%`;
}

export function num(n: number): string {
  return new Intl.NumberFormat().format(n ?? 0);
}

export function shortSha(sha: string): string {
  if (!sha) return "—";
  return sha.length > 7 ? sha.slice(0, 7) : sha;
}

export function relativeTime(iso?: string): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "—";
  const secs = Math.round((Date.now() - then) / 1000);
  const abs = Math.abs(secs);
  const units: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "second"],
    [60, "minute"],
    [24, "hour"],
    [7, "day"],
    [4.35, "week"],
    [12, "month"],
    [Number.POSITIVE_INFINITY, "year"],
  ];
  let value = abs;
  let unit: Intl.RelativeTimeFormatUnit = "second";
  for (const [size, u] of units) {
    if (value < size) {
      unit = u;
      break;
    }
    value = Math.floor(value / size);
  }
  const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  return rtf.format(-Math.sign(secs) * value || -value, unit);
}

export function absTime(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString();
}

export const TITLE: Record<string, string> = {
  unverified: "Unverified",
  "statically-proven": "Statically proven",
  reproduced: "Reproduced",
  cosmetic: "Cosmetic",
  degraded: "Degraded",
  "data-loss": "Data loss",
  exploitable: "Exploitable",
  correctness: "Correctness",
  contract: "Contract",
  resilience: "Resilience",
  security: "Security",
  predicted: "Predicted",
  verified: "Verified",
  "shipped-anyway": "Shipped anyway",
  manifested: "Manifested",
  attributed: "Attributed",
  fixed: "Fixed",
  closed: "Closed",
  dismissed: "Dismissed",
};

export function label(key: string): string {
  return TITLE[key] ?? key;
}

/** Rank used for client-side sorting: verification first, severity second. */
const CONFIDENCE_RANK: Record<Confidence, number> = {
  unverified: 0,
  "statically-proven": 1,
  reproduced: 2,
};
const IMPACT_RANK: Record<Impact, number> = {
  cosmetic: 0,
  degraded: 1,
  "data-loss": 2,
  exploitable: 3,
};

export function confidenceRank(c: Confidence): number {
  return CONFIDENCE_RANK[c] ?? 0;
}
export function impactRank(i: Impact): number {
  return IMPACT_RANK[i] ?? 0;
}
