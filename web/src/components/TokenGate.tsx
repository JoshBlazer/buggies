import { useState, useSyncExternalStore } from "react";
import type { ReactNode } from "react";
import { getLastError, getToken, setToken, subscribe } from "../api/auth";

// The dashboard is served only on the daemon's loopback admin mux, and that mux
// is mounted only when BUGGIES_ADMIN_TOKEN is set (fail closed). The token is a
// defence-in-depth second factor on top of the loopback bind. This gate holds
// the SPA behind the token: nothing calls /api until a token is entered, and a
// 401 from any request drops the user back here with a reason.
export function TokenGate({ children }: { children: ReactNode }) {
  const token = useSyncExternalStore(subscribe, getToken, () => null);
  const lastError = useSyncExternalStore(subscribe, getLastError, () => null);

  if (token) return <>{children}</>;

  return <TokenForm error={lastError} />;
}

function TokenForm({ error }: { error: string | null }) {
  const [value, setValue] = useState("");

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (value.trim()) setToken(value);
  }

  return (
    <div className="gate">
      <form className="gate-card" onSubmit={submit}>
        <div className="brand" style={{ padding: "0 0 8px" }}>
          <span className="brand-mark">Buggies</span>
          <span className="brand-tag">admin</span>
        </div>
        <h2 style={{ fontSize: 16, marginBottom: 4 }}>Enter admin token</h2>
        <p className="help" style={{ marginBottom: 14 }}>
          This console talks to the loopback admin API, which requires the shared{" "}
          <code>BUGGIES_ADMIN_TOKEN</code>. It is sent as a bearer header and kept only in this
          browser — never in a cookie.
        </p>
        {error && (
          <div className="error" style={{ padding: "10px 0", textAlign: "left" }}>
            {error}
          </div>
        )}
        <input
          type="password"
          autoFocus
          value={value}
          placeholder="BUGGIES_ADMIN_TOKEN"
          onChange={(e) => setValue(e.target.value)}
          style={{ width: "100%", minWidth: 0, marginBottom: 12 }}
        />
        <button className="btn" type="submit" disabled={!value.trim()} style={{ width: "100%" }}>
          Unlock console
        </button>
      </form>
    </div>
  );
}
