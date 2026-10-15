import { useEffect, useState } from "react";
import { useSaveBudget, useSettings } from "../api/hooks";
import { Query } from "../components/States";
import { money } from "../lib/format";

export function Settings() {
  const q = useSettings();
  const save = useSaveBudget();
  const [dollars, setDollars] = useState("");

  useEffect(() => {
    if (q.data) setDollars((q.data.editable.budgetCents / 100).toFixed(2));
  }, [q.data]);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const cents = Math.round(Number(dollars) * 100);
    if (Number.isNaN(cents) || cents < 0) return;
    save.mutate(cents);
  }

  const cents = Math.round(Number(dollars) * 100);
  const invalid = Number.isNaN(cents) || cents < 0;

  return (
    <>
      <div className="page-head">
        <h1>Settings</h1>
        <p>
          The monthly budget cap is the only live-editable setting. Everything else is read-only
          daemon config; secrets are never exposed.
        </p>
      </div>

      <Query {...q} what="settings">
        {(s) => (
          <div className="grid-2">
            <div className="panel">
              <div className="panel-head">
                <h2>Monthly budget cap</h2>
              </div>
              <form className="panel-body" onSubmit={submit}>
                <p className="help" style={{ marginTop: 0 }}>
                  Per-repo cap for the current period. Takes effect immediately and is persisted as
                  an override so it survives a restart. Set 0 for no cap.
                </p>
                <div className="field" style={{ marginTop: 12 }}>
                  <label>Cap (USD)</label>
                  <input
                    type="number"
                    min="0"
                    step="0.01"
                    value={dollars}
                    onChange={(e) => setDollars(e.target.value)}
                  />
                  <div className="help">
                    {invalid ? "Must be a non-negative number." : `= ${money(cents)} (${cents} cents)`}
                  </div>
                </div>
                <button
                  className="btn"
                  type="submit"
                  disabled={invalid || save.isPending}
                  style={{ marginTop: 6 }}
                >
                  {save.isPending ? "Saving…" : "Save cap"}
                </button>
                {save.isError && (
                  <div className="help" style={{ color: "var(--exploitable)" }}>
                    {String((save.error as Error)?.message ?? save.error)}
                  </div>
                )}
                {save.isSuccess && (
                  <div className="help" style={{ color: "var(--reproduced)" }}>
                    Saved {money(save.data.budgetCents)}.
                  </div>
                )}
              </form>
            </div>

            <div className="panel">
              <div className="panel-head">
                <h2>Daemon configuration</h2>
              </div>
              <div className="panel-body">
                <div className="meta-list">
                  <Row k="Public address" v={s.readOnly.addr} mono />
                  <Row k="Worker interval" v={`${s.readOnly.intervalSeconds}s`} />
                  <Row k="GitHub host" v={s.readOnly.githubHost} />
                  <Row k="GitHub App ID" v={String(s.readOnly.githubAppID || "—")} />
                  <Row k="Skills" v={s.readOnly.skillsEnabled ? "enabled" : "off"} />
                  <Row k="runc allowed" v={s.readOnly.allowRunc ? "yes" : "no"} />
                  <Row k="Kernel" v={s.readOnly.kernelVersion || "—"} />
                  <Row k="Data root" v={s.readOnly.dataRoot} mono />
                </div>
              </div>
            </div>

            <div className="panel">
              <div className="panel-head">
                <h2>Secrets</h2>
              </div>
              <div className="panel-body">
                <p className="help" style={{ marginTop: 0 }}>
                  Presence only — values never leave the daemon.
                </p>
                <div className="meta-list">
                  <SecretRow k="Webhook secret" present={s.hasWebhookSecret} />
                  <SecretRow k="GitHub App key" present={s.hasAppKey} />
                  <SecretRow k="Store key" present={s.hasStoreKey} />
                </div>
              </div>
            </div>
          </div>
        )}
      </Query>
    </>
  );
}

function Row({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="meta-row">
      <span className="k">{k}</span>
      <span
        className="v"
        style={mono ? { fontFamily: "var(--mono)", fontSize: 12, wordBreak: "break-all" } : undefined}
      >
        {v || "—"}
      </span>
    </div>
  );
}

function SecretRow({ k, present }: { k: string; present: boolean }) {
  return (
    <div className="meta-row">
      <span className="k">{k}</span>
      <span className={`pill ${present ? "pill-success" : "pill-neutral"}`}>
        {present ? "configured" : "not set"}
      </span>
    </div>
  );
}
