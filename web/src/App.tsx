import { FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import maplibregl, { GeoJSONSource, Map as MapLibreMap, Marker } from 'maplibre-gl';

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
  connected?: boolean;
  location?: LocationSample;
};

type FleetResponse = { vehicles: Vehicle[]; server_time: string };
type HistoryResponse = { vehicle_id: string; from: string; to: string; locations: LocationSample[] };

type LiveMessage = {
  type: string;
  location?: LocationSample;
  vehicle_id?: string;
  connected?: boolean;
};

type Freshness = 'LIVE' | 'DELAYED' | 'STALE' | 'OFFLINE' | 'NO DATA';

const EMPTY_GEOJSON = { type: 'FeatureCollection' as const, features: [] };

function freshnessFromTime(recordedAt?: string): Freshness {
  if (!recordedAt) return 'NO DATA';
  const ageSeconds = (Date.now() - new Date(recordedAt).getTime()) / 1000;
  if (ageSeconds < 5) return 'LIVE';
  if (ageSeconds < 30) return 'DELAYED';
  if (ageSeconds < 120) return 'STALE';
  return 'OFFLINE';
}

function freshness(vehicle: Vehicle): Freshness {
  if (vehicle.connected) return 'LIVE';
  return freshnessFromTime(vehicle.location?.recorded_at);
}

function ageLabel(recordedAt?: string): string {
  if (!recordedAt) return 'Never';
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(recordedAt).getTime()) / 1000));
  if (seconds < 5) return 'Now';
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  return `${Math.floor(minutes / 60)}h ago`;
}

function formatSpeed(speed?: number): string {
  return speed == null ? '—' : `${Math.round(speed * 3.6)} km/h`;
}

function formatBearing(bearing?: number): string {
  if (bearing == null) return '—';
  const normalized = ((bearing % 360) + 360) % 360;
  const points = ['N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW'];
  const point = points[Math.round(normalized / 45) % 8];
  return `${point} ${Math.round(normalized)}°`;
}

function formatCoordinates(location?: LocationSample): string {
  if (!location) return 'No position';
  return `${location.latitude.toFixed(5)}, ${location.longitude.toFixed(5)}`;
}

function formatPlaybackTime(sample?: LocationSample): string {
  if (!sample) return 'No history';
  return new Intl.DateTimeFormat(undefined, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  }).format(new Date(sample.recorded_at));
}

function BrandMark({ compact = false }: { compact?: boolean }) {
  return (
    <div className={`brand ${compact ? 'compact' : ''}`} aria-label="EMS Tracker">
      <svg className="brand-mark" viewBox="0 0 28 28" aria-hidden="true">
        <path d="M14 3.5c-4.2 0-7.6 3.3-7.6 7.5 0 5.5 7.6 13.5 7.6 13.5S21.6 16.5 21.6 11c0-4.2-3.4-7.5-7.6-7.5Z" fill="currentColor" />
        <circle cx="14" cy="11" r="3.1" fill="#08111f" />
        <path d="M4 21.5c2.4-1.2 4.5-1.2 6.3 0 2.1 1.4 4.3 1.4 6.5 0 2-1.3 4.4-1.3 7.2.1" fill="none" stroke="#f04444" strokeWidth="2" strokeLinecap="round" />
      </svg>
      <div>
        <strong>EMS Tracker</strong>
        {!compact && <span>Saint Kitts &amp; Nevis</span>}
      </div>
    </div>
  );
}

function MapView({
  vehicles,
  selectedVehicleId,
  history,
  playbackIndex,
  onSelect
}: {
  vehicles: Vehicle[];
  selectedVehicleId: string | null;
  history: LocationSample[];
  playbackIndex: number;
  onSelect: (vehicleId: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<MapLibreMap | null>(null);
  const markersRef = useRef(new Map<string, Marker>());
  const previousSelectionRef = useRef<string | null>(null);
  const [styleReady, setStyleReady] = useState(false);

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;

    const map = new maplibregl.Map({
      container: containerRef.current,
      center: [-62.76, 17.29],
      zoom: 10.2,
      style: 'https://tiles.openfreemap.org/styles/dark'
    });
    map.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'top-right');
    map.addControl(new maplibregl.AttributionControl({ compact: true }));
    map.on('load', () => {
      map.addSource('history-route', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addSource('history-progress', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addSource('playback-point', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addLayer({
        id: 'history-route',
        type: 'line',
        source: 'history-route',
        paint: { 'line-color': '#526174', 'line-width': 3, 'line-opacity': 0.7 }
      });
      map.addLayer({
        id: 'history-progress',
        type: 'line',
        source: 'history-progress',
        paint: { 'line-color': '#24c7b6', 'line-width': 4, 'line-opacity': 0.95 }
      });
      map.addLayer({
        id: 'playback-point',
        type: 'circle',
        source: 'playback-point',
        paint: {
          'circle-radius': 7,
          'circle-color': '#24c7b6',
          'circle-stroke-color': '#08111f',
          'circle-stroke-width': 3
        }
      });
      setStyleReady(true);
    });
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
      const state = freshness(vehicle).toLowerCase().replace(' ', '-');
      let marker = markersRef.current.get(vehicle.vehicle_id);
      if (!marker) {
        const element = document.createElement('button');
        element.type = 'button';
        element.className = 'vehicle-marker';
        element.setAttribute('aria-label', `Select ${vehicle.vehicle_code}`);
        const dot = document.createElement('span');
        dot.className = 'marker-dot';
        const label = document.createElement('span');
        label.className = 'marker-label';
        element.append(dot, label);
        element.addEventListener('click', () => onSelect(vehicle.vehicle_id));
        marker = new maplibregl.Marker({ element, anchor: 'center' })
          .setLngLat([vehicle.location.longitude, vehicle.location.latitude])
          .addTo(map);
        markersRef.current.set(vehicle.vehicle_id, marker);
      } else {
        marker.setLngLat([vehicle.location.longitude, vehicle.location.latitude]);
      }
      const element = marker.getElement();
      element.className = `vehicle-marker ${state}${selectedVehicleId === vehicle.vehicle_id ? ' selected' : ''}`;
      const label = element.querySelector('.marker-label');
      if (label) label.textContent = vehicle.vehicle_code;
    }

    for (const [id, marker] of markersRef.current.entries()) {
      if (!active.has(id)) {
        marker.remove();
        markersRef.current.delete(id);
      }
    }
  }, [vehicles, selectedVehicleId, onSelect]);

  useEffect(() => {
    if (!styleReady) return;
    const map = mapRef.current;
    if (!map) return;

    const coordinates = history.map((sample) => [sample.longitude, sample.latitude]);
    const progressCoordinates = playbackIndex >= 0 ? coordinates.slice(0, playbackIndex + 1) : [];
    const selectedSample = playbackIndex >= 0 ? history[playbackIndex] : undefined;

    const route = map.getSource('history-route') as GeoJSONSource | undefined;
    const progress = map.getSource('history-progress') as GeoJSONSource | undefined;
    const point = map.getSource('playback-point') as GeoJSONSource | undefined;

    route?.setData(coordinates.length > 1 ? {
      type: 'Feature',
      properties: {},
      geometry: { type: 'LineString', coordinates }
    } : EMPTY_GEOJSON);
    progress?.setData(progressCoordinates.length > 1 ? {
      type: 'Feature',
      properties: {},
      geometry: { type: 'LineString', coordinates: progressCoordinates }
    } : EMPTY_GEOJSON);
    point?.setData(selectedSample ? {
      type: 'Feature',
      properties: {},
      geometry: { type: 'Point', coordinates: [selectedSample.longitude, selectedSample.latitude] }
    } : EMPTY_GEOJSON);
  }, [history, playbackIndex, styleReady]);

  useEffect(() => {
    if (!styleReady || history.length < 2) return;
    const map = mapRef.current;
    if (!map) return;
    const bounds = new maplibregl.LngLatBounds();
    history.forEach((sample) => bounds.extend([sample.longitude, sample.latitude]));
    map.fitBounds(bounds, { padding: 70, duration: 500, maxZoom: 15 });
  }, [history, styleReady]);

  useEffect(() => {
    if (previousSelectionRef.current === selectedVehicleId) return;
    previousSelectionRef.current = selectedVehicleId;
    if (!selectedVehicleId) return;
    const selected = vehicles.find((vehicle) => vehicle.vehicle_id === selectedVehicleId);
    if (!selected?.location) return;
    mapRef.current?.easeTo({
      center: [selected.location.longitude, selected.location.latitude],
      zoom: Math.max(mapRef.current.getZoom(), 12),
      duration: 450
    });
  }, [selectedVehicleId, vehicles]);

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
        <BrandMark />
        <div className="login-copy">
          <h1>Dispatcher sign in</h1>
          <p>Live vehicle telemetry and route playback.</p>
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
        <button className="primary-button" type="submit" disabled={submitting}>{submitting ? 'Signing in…' : 'Sign in'}</button>
      </form>
    </main>
  );
}

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [vehicles, setVehicles] = useState<Vehicle[]>([]);
  const [socketUp, setSocketUp] = useState(false);
  const [selectedVehicleId, setSelectedVehicleId] = useState<string | null>(null);
  const [history, setHistory] = useState<LocationSample[]>([]);
  const [historyHours, setHistoryHours] = useState(1);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');
  const [playbackIndex, setPlaybackIndex] = useState(-1);
  const [playing, setPlaying] = useState(false);
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
      const hydrated = body.vehicles.map((vehicle) => ({
        ...vehicle,
        connected: freshnessFromTime(vehicle.location?.recorded_at) === 'LIVE'
      }));
      setVehicles(hydrated);
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
    if (!selectedVehicleId && vehicles.length > 0) {
      setSelectedVehicleId(vehicles[0].vehicle_id);
    }
  }, [selectedVehicleId, vehicles]);

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
          if (message.type === 'presence' && message.vehicle_id) {
            setVehicles((current) => current.map((vehicle) =>
              vehicle.vehicle_id === message.vehicle_id ? { ...vehicle, connected: Boolean(message.connected) } : vehicle
            ));
            return;
          }
          if (message.type !== 'location' || !message.location?.vehicle_id) return;
          const sample = message.location;
          setVehicles((current) => current.map((vehicle) =>
            vehicle.vehicle_id === sample.vehicle_id ? { ...vehicle, connected: true, location: sample } : vehicle
          ));
        } catch {
          // Ignore malformed frames; transport failures are handled by reconnect logic.
        }
      };
      socket.onclose = () => {
        setSocketUp(false);
        setVehicles((current) => current.map((vehicle) => ({ ...vehicle, connected: false })));
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

  useEffect(() => {
    if (!authenticated || !selectedVehicleId) {
      setHistory([]);
      setPlaybackIndex(-1);
      return;
    }
    const controller = new AbortController();
    setHistoryLoading(true);
    setHistoryError('');
    setPlaying(false);

    fetch(`/api/v1/vehicles/${encodeURIComponent(selectedVehicleId)}/history?hours=${historyHours}`, {
      credentials: 'same-origin',
      signal: controller.signal
    })
      .then(async (response) => {
        if (!response.ok) throw new Error('History unavailable');
        return (await response.json()) as HistoryResponse;
      })
      .then((body) => {
        setHistory(body.locations);
        setPlaybackIndex(body.locations.length - 1);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setHistory([]);
        setPlaybackIndex(-1);
        setHistoryError(error instanceof Error ? error.message : 'History unavailable');
      })
      .finally(() => {
        if (!controller.signal.aborted) setHistoryLoading(false);
      });

    return () => controller.abort();
  }, [authenticated, selectedVehicleId, historyHours]);

  useEffect(() => {
    if (!playing || history.length < 2) return;
    const timer = window.setInterval(() => {
      setPlaybackIndex((current) => {
        const step = Math.max(1, Math.ceil(history.length / 300));
        const next = Math.min(history.length - 1, current + step);
        if (next >= history.length - 1) setPlaying(false);
        return next;
      });
    }, 200);
    return () => window.clearInterval(timer);
  }, [playing, history.length]);

  const sortedVehicles = useMemo(
    () => [...vehicles].sort((a, b) => a.vehicle_code.localeCompare(b.vehicle_code)),
    [vehicles]
  );
  const selectedVehicle = useMemo(
    () => vehicles.find((vehicle) => vehicle.vehicle_id === selectedVehicleId) ?? null,
    [vehicles, selectedVehicleId]
  );
  const selectedPlaybackSample = playbackIndex >= 0 ? history[playbackIndex] : undefined;
  const liveCount = vehicles.filter((vehicle) => freshness(vehicle) === 'LIVE').length;
  const offlineCount = vehicles.filter((vehicle) => ['OFFLINE', 'NO DATA'].includes(freshness(vehicle))).length;

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
        <BrandMark compact />
        <div className="fleet-summary" aria-label="Fleet summary">
          <span><strong>{vehicles.length}</strong> units</span>
          <span><strong>{liveCount}</strong> live</span>
          <span><strong>{offlineCount}</strong> offline</span>
        </div>
        <div className="topbar-actions">
          <span className={socketUp ? 'connection live' : 'connection'}>
            <i />{socketUp ? 'Realtime connected' : 'Reconnecting'}
          </span>
          <button className="quiet-button" onClick={() => void logout()}>Sign out</button>
        </div>
      </header>

      <section className="workspace">
        <aside className="fleet-panel">
          <div className="panel-heading">
            <div>
              <span className="section-kicker">FLEET</span>
              <h2>Ambulance units</h2>
            </div>
            <span>{liveCount}/{vehicles.length} live</span>
          </div>
          <div className="vehicle-list">
            {sortedVehicles.map((vehicle) => {
              const state = freshness(vehicle);
              return (
                <button
                  type="button"
                  className={`vehicle-row${selectedVehicleId === vehicle.vehicle_id ? ' selected' : ''}`}
                  key={vehicle.vehicle_id}
                  onClick={() => setSelectedVehicleId(vehicle.vehicle_id)}
                >
                  <div className="vehicle-row-head">
                    <span className={`status-dot ${state.toLowerCase().replace(' ', '-')}`} />
                    <strong>{vehicle.vehicle_code}</strong>
                    <span className={`freshness ${state.toLowerCase().replace(' ', '-')}`}>{state}</span>
                  </div>
                  <div className="vehicle-meta">
                    <span>{vehicle.label || vehicle.status}</span>
                    <span>{ageLabel(vehicle.location?.recorded_at)}</span>
                  </div>
                  <div className="vehicle-quick-data">
                    <span>{formatSpeed(vehicle.location?.speed_mps)}</span>
                    <span>{vehicle.location?.network_type || 'No network'}</span>
                  </div>
                </button>
              );
            })}
            {sortedVehicles.length === 0 && <div className="empty">No vehicles provisioned.</div>}
          </div>
        </aside>

        <section className="map-stage">
          <div className="map-toolbar">
            <div>
              <span className="section-kicker">LIVE MAP</span>
              <strong>{selectedVehicle ? selectedVehicle.vehicle_code : 'Fleet overview'}</strong>
            </div>
            <span className="map-note">OpenStreetMap data · OpenFreeMap tiles</span>
          </div>
          <div className="map-wrap">
            <MapView
              vehicles={vehicles}
              selectedVehicleId={selectedVehicleId}
              history={history}
              playbackIndex={playbackIndex}
              onSelect={setSelectedVehicleId}
            />
          </div>
          <div className="playback-panel">
            <div className="playback-title">
              <div>
                <span className="section-kicker">PLAYBACK</span>
                <strong>{historyLoading ? 'Loading route…' : history.length > 0 ? formatPlaybackTime(selectedPlaybackSample) : 'No route data'}</strong>
              </div>
              <div className="playback-actions">
                <select value={historyHours} onChange={(event) => setHistoryHours(Number(event.target.value))} aria-label="Playback range">
                  <option value={1}>Last hour</option>
                  <option value={6}>Last 6 hours</option>
                  <option value={24}>Last 24 hours</option>
                </select>
                <button
                  className="play-button"
                  disabled={history.length < 2}
                  onClick={() => {
                    if (playbackIndex >= history.length - 1) setPlaybackIndex(0);
                    setPlaying((value) => !value);
                  }}
                >
                  {playing ? 'Pause' : 'Play'}
                </button>
                <button className="quiet-button" disabled={history.length === 0} onClick={() => { setPlaying(false); setPlaybackIndex(history.length - 1); }}>
                  Live
                </button>
              </div>
            </div>
            <input
              className="timeline"
              type="range"
              min={0}
              max={Math.max(0, history.length - 1)}
              value={Math.max(0, playbackIndex)}
              disabled={history.length < 2}
              onChange={(event) => { setPlaying(false); setPlaybackIndex(Number(event.target.value)); }}
              aria-label="Route playback position"
            />
            <div className="timeline-meta">
              <span>{history.length > 0 ? new Date(history[0].recorded_at).toLocaleTimeString() : '—'}</span>
              <span>{historyError || `${history.length.toLocaleString()} sampled points`}</span>
              <span>{history.length > 0 ? new Date(history[history.length - 1].recorded_at).toLocaleTimeString() : '—'}</span>
            </div>
          </div>
        </section>

        <aside className="details-panel">
          {selectedVehicle ? (
            <>
              <div className="details-heading">
                <div>
                  <span className="section-kicker">SELECTED UNIT</span>
                  <h2>{selectedVehicle.vehicle_code}</h2>
                  <p>{selectedVehicle.label}</p>
                </div>
                <span className={`freshness large ${freshness(selectedVehicle).toLowerCase().replace(' ', '-')}`}>{freshness(selectedVehicle)}</span>
              </div>
              <div className="current-position">
                <span>Current position</span>
                <strong>{formatCoordinates(selectedVehicle.location)}</strong>
                <small>{ageLabel(selectedVehicle.location?.recorded_at)}</small>
              </div>
              <dl className="telemetry-grid">
                <div><dt>Speed</dt><dd>{formatSpeed(selectedVehicle.location?.speed_mps)}</dd></div>
                <div><dt>Heading</dt><dd>{formatBearing(selectedVehicle.location?.bearing_deg)}</dd></div>
                <div><dt>Accuracy</dt><dd>{selectedVehicle.location?.accuracy_m == null ? '—' : `±${Math.round(selectedVehicle.location.accuracy_m)} m`}</dd></div>
                <div><dt>Battery</dt><dd>{selectedVehicle.location?.battery_pct == null ? '—' : `${selectedVehicle.location.battery_pct}%`}</dd></div>
                <div><dt>Network</dt><dd>{selectedVehicle.location?.network_type || '—'}</dd></div>
                <div><dt>Node</dt><dd>{selectedVehicle.connected ? 'WebSocket connected' : 'Not connected'}</dd></div>
              </dl>
              <div className="playback-snapshot">
                <div className="section-kicker">PLAYBACK SAMPLE</div>
                {selectedPlaybackSample ? (
                  <>
                    <strong>{formatPlaybackTime(selectedPlaybackSample)}</strong>
                    <span>{formatSpeed(selectedPlaybackSample.speed_mps)} · {formatBearing(selectedPlaybackSample.bearing_deg)}</span>
                    <span>{formatCoordinates(selectedPlaybackSample)}</span>
                  </>
                ) : <span>No history loaded.</span>}
              </div>
            </>
          ) : (
            <div className="empty details-empty">Select a unit to inspect telemetry.</div>
          )}
        </aside>
      </section>
    </main>
  );
}
