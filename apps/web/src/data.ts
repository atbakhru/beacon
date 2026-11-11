export type Incident = {
  id: string;
  service_id: string;
  service: string;
  signature: string;
  title: string;
  severity: string;
  state: string;
  owner: string;
  opened_at: string;
  updated_at: string;
  alert_count: number;
  version: number;
};
export type Service = {
  id: string;
  name: string;
  owner: string;
  tier: string;
  active_incidents: number;
};
export type Alert = {
  id: string;
  source_event_id: string;
  service_id: string;
  service: string;
  signature: string;
  severity: string;
  summary: string;
  seen_at: string;
  processed: boolean;
};
export type TimelineEvent = {
  id: number;
  incident_id: string;
  type: string;
  actor: string;
  payload: { summary?: string; from?: string; to?: string };
  created_at: string;
};
export type Stats = {
  alerts: number;
  pending: number;
  active: number;
  resolved: number;
  indexed: number;
  buckets: { at: string; count: number }[];
  mode: string;
};
export type Match = { incident: Incident; score: number; reason: string };
const names = [
  "checkout-api",
  "payment-service",
  "auth-service",
  "notification-worker",
  "inventory-api",
  "api-gateway",
];
export const scenarios = [
  {
    title: "Database connection pool exhausted",
    signature: "connection-pool",
    service: 0,
    severity: "critical",
  },
  {
    title: "Payment authorization latency elevated",
    signature: "upstream-timeout",
    service: 1,
    severity: "high",
  },
  {
    title: "Token verification failures increasing",
    signature: "token-errors",
    service: 2,
    severity: "high",
  },
  {
    title: "Notification queue processing delayed",
    signature: "queue-lag",
    service: 3,
    severity: "medium",
  },
  {
    title: "Inventory cache hit rate degraded",
    signature: "cache-miss",
    service: 4,
    severity: "medium",
  },
  {
    title: "Upstream 5xx error rate elevated",
    signature: "5xx-rate",
    service: 5,
    severity: "critical",
  },
];
const key = "beacon-demo-v1";
type State = {
  incidents: Incident[];
  services: Service[];
  alerts: Alert[];
  events: TimelineEvent[];
};
function fixture(): State {
  const now = Date.now();
  const services = names.map((name, i) => ({
    id: `b0000000-0000-4000-8000-${String(i + 10).padStart(12, "0")}`,
    name,
    owner: [
      "Commerce",
      "Payments",
      "Identity",
      "Platform",
      "Commerce",
      "Platform",
    ][i],
    tier: "tier-1",
    active_incidents: 0,
  }));
  const incidents: Incident[] = [];
  const alerts: Alert[] = [];
  const events: TimelineEvent[] = [];
  for (let i = 0; i < 20; i++) {
    const s = scenarios[i % 6];
    const old = i >= 5;
    const t = now - (old ? (i + 1) * 3600000 : (i + 2) * 180000);
    const id = `demo-${String(i + 1).padStart(4, "0")}`;
    incidents.push({
      id,
      service_id: services[s.service].id,
      service: names[s.service],
      signature: s.signature,
      title: s.title,
      severity: s.severity,
      state: old ? "resolved" : i === 2 ? "acknowledged" : "open",
      owner: i === 0 ? "" : services[s.service].owner,
      opened_at: new Date(t).toISOString(),
      updated_at: new Date(t + 60000).toISOString(),
      alert_count: 60,
      version: 1,
    });
    for (let j = 0; j < 60; j++)
      alerts.push({
        id: `seed-${i}-${j}`,
        source_event_id: `seed-${i}-${j}`,
        service_id: services[s.service].id,
        service: names[s.service],
        signature: s.signature,
        severity: s.severity,
        summary: s.title,
        seen_at: new Date(t + j * 1000).toISOString(),
        processed: true,
      });
    events.push({
      id: events.length + 1,
      incident_id: id,
      type: "incident.created",
      actor: "correlator",
      payload: { summary: "60 alerts correlated by service and signature" },
      created_at: new Date(t).toISOString(),
    });
    if (old)
      events.push({
        id: events.length + 1,
        incident_id: id,
        type: "incident.resolved",
        actor: "operator",
        payload: { summary: "Service recovered after mitigation" },
        created_at: new Date(t + 240000).toISOString(),
      });
  }
  return { services, incidents, alerts, events };
}
let state: State;
try {
  state = JSON.parse(localStorage.getItem(key) || "null") || fixture();
} catch {
  state = fixture();
}
function save() {
  localStorage.setItem(key, JSON.stringify(state));
  window.dispatchEvent(new Event("beacon-change"));
}
export const demo = import.meta.env.VITE_DEMO_MODE === "true";
const base = import.meta.env.VITE_API_BASE_URL || "";
async function request<T>(
  path: string,
  body?: unknown,
  method?: string,
): Promise<T> {
  const res = await fetch(base + "/v1" + path, {
    method: method || (body ? "POST" : "GET"),
    headers: { "Content-Type": "application/json" },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const e = await res
      .json()
      .catch(() => ({ message: "Backend unavailable" }));
    throw new Error(e.message || "Request failed");
  }
  return res.json();
}
export const api = {
  async incidents() {
    return demo
      ? state.incidents
      : (await request<{ items: Incident[] }>("/incidents?limit=500")).items;
  },
  async services() {
    return demo
      ? state.services.map((s) => ({
          ...s,
          active_incidents: state.incidents.filter(
            (i) => i.service_id === s.id && i.state !== "resolved",
          ).length,
        }))
      : (await request<{ items: Service[] }>("/services")).items;
  },
  async alerts() {
    return demo
      ? [...state.alerts]
          .sort((a, b) => Date.parse(b.seen_at) - Date.parse(a.seen_at))
          .slice(0, 100)
      : (await request<{ items: Alert[] }>("/alerts?limit=100")).items;
  },
  async timeline(id: string) {
    return demo
      ? state.events.filter((e) => e.incident_id === id)
      : (await request<{ items: TimelineEvent[] }>(`/incidents/${id}/timeline`))
          .items;
  },
  async stats(): Promise<Stats> {
    if (!demo) return request("/stats");
    const buckets: Record<string, number> = {};
    state.alerts
      .filter((a) => Date.now() - Date.parse(a.seen_at) < 3600000)
      .forEach((a) => {
        const k = new Date(
          Math.floor(Date.parse(a.seen_at) / 60000) * 60000,
        ).toISOString();
        buckets[k] = (buckets[k] || 0) + 1;
      });
    return {
      alerts: state.alerts.length,
      pending: 0,
      active: state.incidents.filter((i) => i.state !== "resolved").length,
      resolved: state.incidents.filter((i) => i.state === "resolved").length,
      indexed: state.incidents.length,
      buckets: Object.entries(buckets).map(([at, count]) => ({ at, count })),
      mode: "browser demo",
    };
  },
  async transition(id: string, action: string) {
    if (!demo) return request<Incident>(`/incidents/${id}/${action}`, {});
    const i = state.incidents.find((i) => i.id === id)!;
    const from = i.state;
    i.state = { ack: "acknowledged", resolve: "resolved", reopen: "open" }[
      action
    ]!;
    i.updated_at = new Date().toISOString();
    state.events.push({
      id: state.events.length + 1,
      incident_id: id,
      type: `incident.${i.state}`,
      actor: "you",
      payload: { from, to: i.state },
      created_at: i.updated_at,
    });
    save();
    return i;
  },
  async assign(id: string, owner: string) {
    if (!demo) return request(`/incidents/${id}`, { owner }, "PATCH");
    state.incidents.find((i) => i.id === id)!.owner = owner;
    save();
  },
  async similar(i: Incident): Promise<Match[]> {
    if (!demo)
      return (await request<{ items: Match[] }>(`/incidents/${i.id}/similar`))
        .items;
    return state.incidents
      .filter((v) => v.id !== i.id && v.signature === i.signature)
      .map((v) => ({
        incident: v,
        score: 1,
        reason: `Same failure signature · ${v.signature}`,
      }))
      .slice(0, 4);
  },
  async search(query: string): Promise<Match[]> {
    if (!demo)
      return (await request<{ items: Match[] }>("/search/incidents", { query }))
        .items;
    const words = query.toLowerCase().split(/\W+/).filter(Boolean);
    return state.incidents
      .map((i) => ({
        incident: i,
        score:
          words.filter((w) =>
            (i.title + " " + i.service + " " + i.signature)
              .toLowerCase()
              .includes(w),
          ).length / Math.max(words.length, 1),
        reason: "Keyword overlap in this browser demo",
      }))
      .filter((m) => m.score > 0)
      .sort((a, b) => b.score - a.score)
      .slice(0, 8);
  },
  async burst(scenario: number, count: number, duplicate = false) {
    const s = scenarios[scenario];
    const service = (await this.services()).find(
      (v) => v.name === names[s.service],
    )!;
    const batchID = crypto.randomUUID();
    const inputs = Array.from({ length: count }, (_, n) => ({
      source_event_id: duplicate ? "beacon-duplicate-check" : `${batchID}-${n}`,
      service_id: service.id,
      signature: s.signature,
      severity: s.severity,
      summary: s.title,
      seen_at: new Date().toISOString(),
    }));
    if (!demo) {
      const r = await request<{
        items: { duplicate?: boolean; error?: string }[];
      }>("/alerts/batch", { alerts: inputs });
      if (r.items.some((i) => i.error))
        throw new Error("Some alerts were rejected");
      return {
        accepted: r.items.filter((i) => !i.duplicate).length,
        duplicates: r.items.filter((i) => i.duplicate).length,
      };
    }
    let accepted = 0;
    for (const v of inputs) {
      if (state.alerts.some((a) => a.source_event_id === v.source_event_id))
        continue;
      let incident = state.incidents.find(
        (i) =>
          i.service_id === v.service_id &&
          i.signature === v.signature &&
          i.state !== "resolved" &&
          Date.now() - Date.parse(i.updated_at) < 900000,
      );
      if (!incident) {
        incident = {
          id: crypto.randomUUID(),
          service_id: v.service_id,
          service: service.name,
          signature: s.signature,
          title: s.title,
          severity: s.severity,
          state: "open",
          owner: "",
          opened_at: v.seen_at,
          updated_at: v.seen_at,
          alert_count: 0,
          version: 1,
        };
        state.incidents.unshift(incident);
      }
      incident.alert_count++;
      incident.updated_at = v.seen_at;
      state.alerts.push({
        ...v,
        id: crypto.randomUUID(),
        service: service.name,
        processed: true,
      });
      accepted++;
      if (accepted === 1)
        state.events.push({
          id: state.events.length + 1,
          incident_id: incident.id,
          type: "incident.updated",
          actor: "correlator",
          payload: { summary: `Synthetic ${count}-alert burst` },
          created_at: v.seen_at,
        });
    }
    save();
    return { accepted, duplicates: count - accepted };
  },
  reset() {
    state = fixture();
    save();
  },
};
export function connect(onUpdate: () => void, onStatus: (s: string) => void) {
  if (demo) {
    onStatus("Demo live");
    window.addEventListener("beacon-change", onUpdate);
    return () => window.removeEventListener("beacon-change", onUpdate);
  }
  let socket: WebSocket;
  let stopped = false;
  let timer: ReturnType<typeof setTimeout>;
  let cursor = 0;
  let retry = 1000;
  const open = () => {
    onStatus("Connecting");
    const url = new URL(base || location.origin);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    url.pathname = "/v1/stream";
    url.search = `cursor=${cursor}`;
    socket = new WebSocket(url);
    socket.onopen = () => {
      retry = 1000;
      onStatus("Live");
      onUpdate();
    };
    socket.onmessage = (e) => {
      const v = JSON.parse(e.data);
      if (v.cursor) cursor = Math.max(cursor, v.cursor);
      onUpdate();
    };
    socket.onclose = () => {
      onStatus("Reconnecting");
      if (!stopped) {
        timer = setTimeout(open, retry);
        retry = Math.min(retry * 2, 15000);
      }
    };
    socket.onerror = () => socket.close();
  };
  open();
  return () => {
    stopped = true;
    clearTimeout(timer);
    socket?.close();
  };
}
