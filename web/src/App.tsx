import { FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import maplibregl, { Map as MapLibreMap, Marker } from 'maplibre-gl';

type LocationSample = {
  vehicle_id?: string;
  vehicle_code?: string;
  tracking_session_id: string;
  sequence_number: number;
  recorded_at: string;
  latitude: number;
  longitude: number;
  accuracy_m?: number;
  speed_mps?: number;
  bearing_deg?: number;
  altitude_m?: number;
  battery_pct?: number;
  network_type?: string;
};

type Vehicle = {
  vehicle_id: string;
  vehicle_code: string;
  label: string;
  status: string;
  location?: LocationSample;
};

type FleetResponse = { vehicles: Vehicle[]; server_time: string };

type LiveMessage = {
  type: string;
  location?: LocationSample;
  received_at?: string;
  replayed?: boolean;
};

type Freshness = 'LIVE' | 'DELAYED' | 'STALE' | 'OFFLINE' | 'NO DATA';

function freshness(recordedAt?: string): Freshness {
  if (!recordedAt) return 'NO DATA';
  const ageSeconds = (Date.now() - new Date(recordedAt).getTime()) / 1000;
  if (ageSeconds < 5) return 'LIVE';
  if (ageSeconds < 30) return 'DELAYED';
  if (ageSeconds < 120) return 'STALE';
  return 'OFFLINE';
}

function ageLabel(recordedAt?: string): string {
  if (!recordedAt) return 'never';
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(recordedAt).getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  return `${Math.floor(minutes / 60)}h ago`;
}

function MapView({ vehicles }: { vehicles: Vehicle[] }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<MapLibreMap | null>(null);
  const markersRef = useRef(new Map<string, Marker>());

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;

    const map = new maplibregl.Map({
      container: containerRef.current,
      center: [-62.75, 17.31],
      zoom: 11,
      style: {
        version: 8,
        sources: {
          osm: {
            type: 'raster',
            tiles: ['https://tile.openstreetmap.org/{z}/{x}/{y}.png'],
            tileSize: 256,
            attribution: '© OpenStreetMap contributors'
          }
        },
        layers: [{ id: 'osm', type: 'raster', source: 'osm' }]
      }
    });

    map.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right');
    mapRef.current = map;

    return () => {
      markersRef.current.forEach((marker) => marker.remove());
      markersRef.current.clear();
      map.remove();
      mapRef.current = null;
    };
  }, []);

  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;

    const active = new Set<string>();
    for (const vehicle of vehicles) {
      if (!vehicle.location) continue;
      active.add(vehicle.vehicle_id);

      let marker = markersRef.current.get(vehicle.vehicle_id);
      if (!marker) {
        const element = document.createElement('div');
        element.className = 'vehicle-marker';
        element.textContent = vehicle.vehicle_code;
        marker = new maplibregl.Marker({ element, anchor: 'center' })
          .setLngLat([vehicle.location.longitude, vehicle.location.latitude])
          .addTo(map);
        markersRef.current.set(vehicle.vehicle_id, marker);
      } else {
        marker.setLngLat([vehicle.location.longitude, vehicle.location.latitude]);
      }
    }

    for (const [id, marker] of markersRef.current.entries()) {
      if (!active.has(id)) {
        marker.remove();
        markersRef.current.delete(id);
      }
    }
  }, [vehicles]);

  return <div className="map" ref={containerRef} aria-label="Live ambulance map" />;
}

function Login({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      const response = await fetch('/api/v1/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ username, password })
      });
      if (!response.ok) {
        setError('Invalid username or password.');
        return;
      }
      const body = (await response.json()) as { csrf_token: string };
      sessionStorage.setItem('csrf_token', body.csrf_token);
      onAuthenticated();
    } catch {
      setError('Server unavailable.');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="login-page">
      <form className="login-panel" onSubmit={submit}>
        <div>
          <div className="eyebrow">EMS OPERATIONS</div>
          <h1>Ambulance Dispatch</h1>
          <p>Authorized personnel only.</p>
        </div>
        <label>
          Username
          <input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" required />
        </label>
        <label>
          Password
          <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required />
        </label>
        {error && <div className="error" role="alert">{error}</div>}
        <button type="submit" disabled={submitting}>{submitting ? 'Signing in…' : 'Sign in'}</button>
      </form>
    </main>
  );
}

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [vehicles, setVehicles] = useState<Vehicle[]>([]);
  const [socketUp, setSocketUp] = useState(false);
  const [, setClock] = useState(0);

  async function loadFleet() {
    try {
      const response = await fetch('/api/v1/vehicles', { credentials: 'same-origin' });
      if (response.status === 401) {
        setAuthenticated(false);
        setVehicles([]);
        return;
      }
      if (!response.ok) throw new Error('fleet request failed');
      const body = (await response.json()) as FleetResponse;
      setVehicles(body.vehicles);
      setAuthenticated(true);
    } catch {
      setAuthenticated(false);
    }
  }

  useEffect(() => {
    void loadFleet();
  }, []);

  useEffect(() => {
    const timer = window.setInterval(() => setClock((value) => value + 1), 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    if (!authenticated) return;
    let socket: WebSocket | null = null;
    let retryTimer: number | undefined;
    let stopped = false;
    let retryMs = 1000;

    const connect = () => {
      if (stopped) return;
      const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws';
      socket = new WebSocket(`${scheme}://${window.location.host}/api/v1/dispatch/ws`);
      socket.onopen = () => {
        retryMs = 1000;
        setSocketUp(true);
      };
      socket.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data) as LiveMessage;
          if (message.type !== 'location' || !message.location?.vehicle_id) return;
          const sample = message.location;
          setVehicles((current) => current.map((vehicle) =>
            vehicle.vehicle_id === sample.vehicle_id ? { ...vehicle, location: sample } : vehicle
          ));
        } catch {
          // Ignore malformed server frames; reconnect logic handles transport failures.
        }
      };
      socket.onclose = () => {
        setSocketUp(false);
        if (!stopped) {
          retryTimer = window.setTimeout(connect, retryMs);
          retryMs = Math.min(retryMs * 2, 10000);
        }
      };
      socket.onerror = () => socket?.close();
    };

    connect();
    return () => {
      stopped = true;
      setSocketUp(false);
      if (retryTimer) window.clearTimeout(retryTimer);
      socket?.close();
    };
  }, [authenticated]);

  const sortedVehicles = useMemo(
    () => [...vehicles].sort((a, b) => a.vehicle_code.localeCompare(b.vehicle_code)),
    [vehicles]
  );

  async function logout() {
    const csrf = sessionStorage.getItem('csrf_token') ?? '';
    await fetch('/api/v1/auth/logout', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-CSRF-Token': csrf }
    }).catch(() => undefined);
    sessionStorage.removeItem('csrf_token');
    setAuthenticated(false);
    setVehicles([]);
  }

  if (authenticated === null) {
    return <div className="boot">Connecting…</div>;
  }
  if (!authenticated) {
    return <Login onAuthenticated={() => void loadFleet()} />;
  }

  return (
    <main className="app-shell">
      <header className="topbar">
        <div>
          <div className="eyebrow">EMS OPERATIONS</div>
          <strong>Ambulance Dispatch</strong>
        </div>
        <div className="topbar-actions">
          <span className={socketUp ? 'connection live' : 'connection'}>{socketUp ? 'Live feed' : 'Reconnecting'}</span>
          <button className="secondary" onClick={() => void logout()}>Sign out</button>
        </div>
      </header>

      <section className="workspace">
        <aside className="fleet-panel">
          <div className="panel-heading">
            <h2>Fleet</h2>
            <span>{sortedVehicles.length} units</span>
          </div>
          <div className="vehicle-list">
            {sortedVehicles.map((vehicle) => {
              const state = freshness(vehicle.location?.recorded_at);
              const speed = vehicle.location?.speed_mps == null ? '—' : `${Math.round(vehicle.location.speed_mps * 3.6)} km/h`;
              const accuracy = vehicle.location?.accuracy_m == null ? '—' : `±${Math.round(vehicle.location.accuracy_m)} m`;
              return (
                <article className="vehicle-row" key={vehicle.vehicle_id}>
                  <div className="vehicle-row-head">
                    <strong>{vehicle.vehicle_code}</strong>
                    <span className={`freshness ${state.toLowerCase().replace(' ', '-')}`}>{state}</span>
                  </div>
                  <div className="vehicle-meta">
                    <span>{vehicle.status}</span>
                    <span>{ageLabel(vehicle.location?.recorded_at)}</span>
                  </div>
                  <dl>
                    <div><dt>Speed</dt><dd>{speed}</dd></div>
                    <div><dt>Accuracy</dt><dd>{accuracy}</dd></div>
                    <div><dt>Battery</dt><dd>{vehicle.location?.battery_pct == null ? '—' : `${vehicle.location.battery_pct}%`}</dd></div>
                    <div><dt>Network</dt><dd>{vehicle.location?.network_type || '—'}</dd></div>
                  </dl>
                </article>
              );
            })}
            {sortedVehicles.length === 0 && <div className="empty">No vehicles provisioned.</div>}
          </div>
        </aside>
        <MapView vehicles={sortedVehicles} />
      </section>
    </main>
  );
}
