import type { ReactNode } from "react";

export function Loading({ what = "data" }: { what?: string }) {
  return (
    <div className="loading">
      <span className="spinner" /> Loading {what}…
    </div>
  );
}

export function ErrorState({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div className="error">
      <div className="empty-title">Could not reach the daemon</div>
      <div>{message}</div>
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {children}
    </div>
  );
}

/** Convenience: render loading / error / empty / content for a query result. */
export function Query<T>({
  isLoading,
  error,
  data,
  what,
  empty,
  children,
}: {
  isLoading: boolean;
  error: unknown;
  data: T | undefined;
  what?: string;
  empty?: ReactNode;
  children: (data: T) => ReactNode;
}) {
  if (isLoading) return <Loading what={what} />;
  if (error) return <ErrorState error={error} />;
  if (data === undefined) return <>{empty ?? <Empty title="Nothing here yet" />}</>;
  return <>{children(data)}</>;
}
