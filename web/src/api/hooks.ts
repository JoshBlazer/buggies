// Data-fetching hooks. React Query owns caching, refetch-on-focus, loading and
// error state; components stay declarative and never touch fetch directly.
//
// Every endpoint here maps 1:1 to the daemon's admin API spec.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type {
  Budget,
  FindingDetail,
  FindingHistory,
  FindingsResponse,
  JobDetail,
  JobsPage,
  Overview,
  ReposResponse,
  Settings,
  TransitionResult,
} from "./types";

export interface JobFilters {
  state?: string;
  repo?: string;
  limit?: number;
  offset?: number;
}

export interface FindingFilters {
  repo: string; // required: findings.db is per-repo
  path?: string;
  type?: string;
  state?: string;
  minConfidence?: string;
  limit?: number;
}

const keys = {
  overview: ["overview"] as const,
  jobs: (f: JobFilters) => ["jobs", f] as const,
  job: (id: number | string) => ["job", String(id)] as const,
  repos: ["repos"] as const,
  findings: (f: FindingFilters) => ["findings", f] as const,
  finding: (repo: string, id: string) => ["finding", repo, id] as const,
  history: (repo: string, id: string) => ["finding-history", repo, id] as const,
  budget: ["budget"] as const,
  settings: ["settings"] as const,
};

export function useOverview() {
  return useQuery({
    queryKey: keys.overview,
    queryFn: () => api.get<Overview>("overview"),
    refetchInterval: 15_000,
  });
}

export function useJobs(filters: JobFilters) {
  return useQuery({
    queryKey: keys.jobs(filters),
    queryFn: () => api.get<JobsPage>("jobs", { ...filters }),
    refetchInterval: 10_000,
  });
}

export function useJob(id: number | string) {
  return useQuery({
    queryKey: keys.job(id),
    queryFn: () => api.get<JobDetail>(`jobs/${id}`),
    enabled: id !== "" && id !== undefined,
  });
}

export function useRequeueJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number | string) => api.post<{ id: number; state: string }>(`jobs/${id}/requeue`),
    onSuccess: (_res, id) => {
      void qc.invalidateQueries({ queryKey: keys.job(id) });
      void qc.invalidateQueries({ queryKey: ["jobs"] });
      void qc.invalidateQueries({ queryKey: keys.overview });
    },
  });
}

export function useRepos() {
  return useQuery({
    queryKey: keys.repos,
    queryFn: () => api.get<ReposResponse>("repos"),
    refetchInterval: 20_000,
  });
}

export function useFindings(filters: FindingFilters) {
  return useQuery({
    queryKey: keys.findings(filters),
    queryFn: () => api.get<FindingsResponse>("findings", { ...filters }),
    enabled: Boolean(filters.repo),
  });
}

export function useFinding(repo: string, id: string) {
  return useQuery({
    queryKey: keys.finding(repo, id),
    queryFn: () => api.get<FindingDetail>(`findings/${id}`, { repo }),
    enabled: Boolean(repo && id),
  });
}

export function useFindingHistory(repo: string, id: string) {
  return useQuery({
    queryKey: keys.history(repo, id),
    queryFn: () => api.get<FindingHistory>(`findings/${id}/history`, { repo }),
    enabled: Boolean(repo && id),
  });
}

export interface TransitionInput {
  id: string;
  repo: string;
  to: string;
  reason: string;
}

export function useTransitionFinding() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, repo, to, reason }: TransitionInput) =>
      api.post<TransitionResult>(`findings/${id}/transition`, { repo, to, reason }),
    onSuccess: (_res, { repo, id }) => {
      void qc.invalidateQueries({ queryKey: keys.finding(repo, id) });
      void qc.invalidateQueries({ queryKey: keys.history(repo, id) });
      void qc.invalidateQueries({ queryKey: ["findings"] });
      void qc.invalidateQueries({ queryKey: keys.overview });
    },
  });
}

export function useBudget() {
  return useQuery({
    queryKey: keys.budget,
    queryFn: () => api.get<Budget>("budget"),
    refetchInterval: 20_000,
  });
}

export function useSettings() {
  return useQuery({
    queryKey: keys.settings,
    queryFn: () => api.get<Settings>("settings"),
  });
}

export function useSaveBudget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (budgetCents: number) =>
      api.put<{ budgetCents: number }>("settings", { budgetCents }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.settings });
      void qc.invalidateQueries({ queryKey: keys.budget });
      void qc.invalidateQueries({ queryKey: keys.overview });
    },
  });
}
