export function Meter({ value, limit }: { value: number; limit: number }) {
  // limit <= 0 means "no cap": show a flat, un-filled track.
  const ratio = limit > 0 ? Math.min(value / limit, 1) : 0;
  const tone = limit <= 0 ? "" : ratio >= 1 ? " over" : ratio >= 0.8 ? " warn" : "";
  return (
    <div className="meter" role="progressbar" aria-valuenow={Math.round(ratio * 100)}>
      <div className={`meter-fill${tone}`} style={{ width: `${ratio * 100}%` }} />
    </div>
  );
}
