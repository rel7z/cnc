"use client";

export type NetworkSnapshot = {
  sampled_at: number;
  ready: boolean;
  rx_bytes_per_second: number;
  tx_bytes_per_second: number;
  interfaces: { name: string; rx_bytes: number; tx_bytes: number; rx_bytes_per_second: number; tx_bytes_per_second: number }[];
};
function rate(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "—";
  const units = ["B/s", "KiB/s", "MiB/s", "GiB/s"];
  let index = 0;
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index++; }
  return `${value.toFixed(index ? 1 : 0)} ${units[index]}`;
}
export function WorkerNetwork({ network }: { network?: NetworkSnapshot }) {
  if (!network) return <div className="mt-1 text-xs text-gray-500">Network unavailable</div>;
  if (Date.now() - network.sampled_at > 15000) return <div className="mt-1 text-xs text-amber-400">Network reading stale</div>;
  if (!network.ready) return <div className="mt-1 text-xs text-gray-500">Network sampling…</div>;
  const details = network.interfaces.map(n => `${n.name}: ↓ ${rate(n.rx_bytes_per_second)} / ↑ ${rate(n.tx_bytes_per_second)}`).join("\n");
  return <div className="mt-1 text-xs tabular-nums text-sky-300" title={`Host traffic across non-loopback interfaces. Tunnel/bridge traffic can be counted on multiple interfaces.\n${details}`}>
    <span aria-label="Download rate">↓ {rate(network.rx_bytes_per_second)}</span>
    <span className="mx-2 text-gray-600">/</span>
    <span aria-label="Upload rate">↑ {rate(network.tx_bytes_per_second)}</span>
  </div>;
}
