import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
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
type FleetFilter = 'ALL' | 'LIVE' | 'OFFLINE';
type Destination = { latitude: number; longitude: number };
type RoutePlan = {
  vehicle_id: string;
  origin: { latitude: number; longitude: number; accuracy_m?: number };
  destination: Destination;
  distance_m: number;
  duration_seconds: number;
  eta_at: string;
  method: string;
  approximate: boolean;
  geometry: { type: 'LineString'; coordinates: number[][] };
  location_recorded_at: string;
  generated_at: string;
};

const MAP_STYLE = 'https://tiles.openfreemap.org/styles/liberty';
const EMPTY_GEOJSON = { type: 'FeatureCollection' as const, features: [] };
const UNIT_ICON_PATH = 'M10 3h4v7h7v4h-7v7h-4v-7H3v-4h7V3Z';
const UNIT_ICON_SVG = `<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="${UNIT_ICON_PATH}"/></svg>`;

function UnitIcon() {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d={UNIT_ICON_PATH} />
    </svg>
  );
}

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

function formatETA(seconds: number): string {
  if (seconds < 60) return '<1 min';
  const minutes = Math.max(1, Math.round(seconds / 60));
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.floor(minutes / 60);
  const remainder = minutes % 60;
  return remainder === 0 ? `${hours} hr` : `${hours} hr ${remainder} min`;
}

function formatDistance(meters: number): string {
  if (meters < 1000) return `${Math.round(meters)} m`;
  return `${(meters / 1000).toFixed(meters < 10_000 ? 1 : 0)} km`;
}

function statusClass(value: Freshness): string {
  return value.toLowerCase().replace(' ', '-');
}

function BrandMark({ compact = false }: { compact?: boolean }) {
  return (
    <div className={`brand ${compact ? 'compact' : ''}`} aria-label="EMS Tracker">
      <svg className="brand-mark" viewBox="0 0 32 32" aria-hidden="true">
        <path d="M13 2h6v8l6-5 4 5-7 6 7 6-4 5-6-5v8h-6v-8l-6 5-4-5 7-6-7-6 4-5 6 5V2Z" fill="currentColor" />
        <path d="M16 8v16M12.8 11.5 19.2 20.5M19.2 11.5 12.8 20.5" fill="none" stroke="white" strokeWidth="1.35" strokeLinecap="round" />
      </svg>
      <div>
        <strong>EMS Tracker</strong>
        {!compact && <span>St. Kitts &amp; Nevis</span>}
      </div>
    </div>
  );
}

function MapView({
  vehicles,
  selectedVehicleId,
  history,
  playbackIndex,
  focusRequest,
  planningRoute,
  routePlan,
  routeDestination,
  onSelect,
  onDestination
}: {
  vehicles: Vehicle[];
  selectedVehicleId: string | null;
  history: LocationSample[];
  playbackIndex: number;
  focusRequest: number;
  planningRoute: boolean;
  routePlan: RoutePlan | null;
  routeDestination: Destination | null;
  onSelect: (vehicleId: string) => void;
  onDestination: (destination: Destination) => void;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<MapLibreMap | null>(null);
  const markersRef = useRef(new Map<string, Marker>());
  const planningRef = useRef(planningRoute);
  const destinationCallbackRef = useRef(onDestination);
  const fittedDestinationRef = useRef('');
  const [styleReady, setStyleReady] = useState(false);

  useEffect(() => { planningRef.current = planningRoute; }, [planningRoute]);
  useEffect(() => { destinationCallbackRef.current = onDestination; }, [onDestination]);

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;

    const map = new maplibregl.Map({
      container: containerRef.current,
      center: [-62.76, 17.29],
      zoom: 10.15,
      style: MAP_STYLE,
      attributionControl: false
    });

    map.addControl(new maplibregl.NavigationControl({ showCompass: true }), 'bottom-right');
    map.addControl(new maplibregl.AttributionControl({ compact: true }), 'bottom-right');
    map.on('load', () => {
      map.addSource('history-route', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addSource('history-progress', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addSource('playback-point', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addSource('planned-route', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addSource('route-destination', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addLayer({
        id: 'history-route',
        type: 'line',
        source: 'history-route',
        paint: { 'line-color': '#5f6368', 'line-width': 4, 'line-opacity': 0.38 }
      });
      map.addLayer({
        id: 'history-progress',
        type: 'line',
        source: 'history-progress',
        paint: { 'line-color': '#1a73e8', 'line-width': 5, 'line-opacity': 0.95 }
      });
      map.addLayer({
        id: 'playback-point',
        type: 'circle',
        source: 'playback-point',
        paint: {
          'circle-radius': 7,
          'circle-color': '#1a73e8',
          'circle-stroke-color': '#ffffff',
          'circle-stroke-width': 3
        }
      });
      map.addLayer({
        id: 'planned-route-casing',
        type: 'line',
        source: 'planned-route',
        paint: { 'line-color': '#ffffff', 'line-width': 9, 'line-opacity': 0.9 }
      });
      map.addLayer({
        id: 'planned-route-line',
        type: 'line',
        source: 'planned-route',
        paint: { 'line-color': '#1a73e8', 'line-width': 6, 'line-opacity': 0.95 }
      });
      map.addLayer({
        id: 'route-destination',
        type: 'circle',
        source: 'route-destination',
        paint: {
          'circle-radius': 8,
          'circle-color': '#d93025',
          'circle-stroke-color': '#ffffff',
          'circle-stroke-width': 3
        }
      });
      setStyleReady(true);
    });

    const handleClick = (event: maplibregl.MapMouseEvent) => {
      if (!planningRef.current) return;
      destinationCallbackRef.current({ latitude: event.lngLat.lat, longitude: event.lngLat.lng });
    };
    map.on('click', handleClick);
    mapRef.current = map;

    return () => {
      markersRef.current.forEach((marker) => marker.remove());
      markersRef.current.clear();
      map.off('click', handleClick);
      map.remove();
      mapRef.current = null;
    };
  }, []);

  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    map.getCanvas().style.cursor = planningRoute ? 'crosshair' : '';
  }, [planningRoute]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;

    const active = new Set<string>();
    for (const vehicle of vehicles) {
      if (!vehicle.location) continue;
      active.add(vehicle.vehicle_id);
      const state = statusClass(freshness(vehicle));
      let marker = markersRef.current.get(vehicle.vehicle_id);

      if (!marker) {
        const element = document.createElement('button');
        element.type = 'button';
        element.className = 'vehicle-marker';
        element.setAttribute('aria-label', `Select ${vehicle.vehicle_code}`);
        const symbol = document.createElement('span');
        symbol.className = 'marker-symbol';
        symbol.innerHTML = UNIT_ICON_SVG;
        const label = document.createElement('span');
        label.className = 'marker-label';
        element.append(symbol, label);
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
      type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates }
    } : EMPTY_GEOJSON);
    progress?.setData(progressCoordinates.length > 1 ? {
      type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: progressCoordinates }
    } : EMPTY_GEOJSON);
    point?.setData(selectedSample ? {
      type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [selectedSample.longitude, selectedSample.latitude] }
    } : EMPTY_GEOJSON);
  }, [history, playbackIndex, styleReady]);

  useEffect(() => {
    if (!styleReady) return;
    const map = mapRef.current;
    if (!map) return;
    const plannedRoute = map.getSource('planned-route') as GeoJSONSource | undefined;
    const destinationSource = map.getSource('route-destination') as GeoJSONSource | undefined;
    const routeCoordinates = routePlan?.geometry.coordinates ?? [];
    const destination = routeDestination ?? routePlan?.destination ?? null;

    plannedRoute?.setData(routeCoordinates.length > 1 ? {
      type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: routeCoordinates }
    } : EMPTY_GEOJSON);
    destinationSource?.setData(destination ? {
      type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [destination.longitude, destination.latitude] }
    } : EMPTY_GEOJSON);

    if (!routePlan || routeCoordinates.length < 2) return;
    const destinationKey = `${routePlan.destination.latitude.toFixed(5)}:${routePlan.destination.longitude.toFixed(5)}`;
    if (fittedDestinationRef.current === destinationKey) return;
    fittedDestinationRef.current = destinationKey;
    const bounds = routeCoordinates.reduce(
      (current, coordinate) => current.extend(coordinate as [number, number]),
      new maplibregl.LngLatBounds(routeCoordinates[0] as [number, number], routeCoordinates[0] as [number, number])
    );
    map.fitBounds(bounds, {
      padding: { left: 310, right: 370, top: 100, bottom: 120 },
      maxZoom: 15,
      duration: 650
    });
  }, [routePlan, routeDestination, styleReady]);

  useEffect(() => {
    if (!selectedVehicleId) return;
    const selected = vehicles.find((vehicle) => vehicle.vehicle_id === selectedVehicleId);
    if (!selected?.location) return;
    mapRef.current?.easeTo({
      center: [selected.location.longitude, selected.location.latitude],
      zoom: Math.max(mapRef.current.getZoom(), 12.3),
      duration: 420,
      padding: { left: 290, right: 350, top: 80, bottom: 120 }
    });
  }, [selectedVehicleId, vehicles, focusRequest]);

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
          <p>Live ambulance locations and route history.</p>
        </div>
        <label>
          <span className="field-label">Username<span className="required-mark" aria-hidden="true">*</span></span>
          <input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" required />
        </label>
        <label>
          <span className="field-label">Password<span className="required-mark" aria-hidden="true">*</span></span>
          <input type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required />
        </label>
        <div className="error" role="alert" aria-live="assertive">{error}</div>
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
  const [historyOpen, setHistoryOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [fleetFilter, setFleetFilter] = useState<FleetFilter>('ALL');
  const [focusRequest, setFocusRequest] = useState(0);
  const [routeMode, setRouteMode] = useState(false);
  const [routeDestination, setRouteDestination] = useState<Destination | null>(null);
  const [routePlan, setRoutePlan] = useState<RoutePlan | null>(null);
  const [routeLoading, setRouteLoading] = useState(false);
  const [routeError, setRouteError] = useState('');
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
      setVehicles(body.vehicles.map((vehicle) => ({
        ...vehicle,
        connected: freshnessFromTime(vehicle.location?.recorded_at) === 'LIVE'
      })));
      setAuthenticated(true);
    } catch {
      setAuthenticated(false);
    }
  }

  const fetchRoute = useCallback(async (
    vehicleID: string,
    destination: Destination,
    quiet = false
  ) => {
    if (!quiet) setRouteLoading(true);
    setRouteError('');
    try {
      const params = new URLSearchParams({
        lat: destination.latitude.toString(),
        lon: destination.longitude.toString()
      });
      const response = await fetch(`/api/v1/vehicles/${encodeURIComponent(vehicleID)}/route?${params}`, {
        credentials: 'same-origin'
      });
      if (response.status === 401) {
        setAuthenticated(false);
        return;
      }
      const body = await response.json().catch(() => ({})) as Partial<RoutePlan> & { error?: string };
      if (!response.ok || !body.geometry || !body.duration_seconds) {
        throw new Error(body.error || 'Route unavailable');
      }
      setRoutePlan(body as RoutePlan);
    } catch (error) {
      if (!quiet) setRoutePlan(null);
      setRouteError(error instanceof Error ? error.message : 'Route unavailable');
    } finally {
      if (!quiet) setRouteLoading(false);
    }
  }, []);

  const chooseRouteDestination = useCallback((destination: Destination) => {
    setRouteDestination(destination);
    setRouteMode(false);
    if (selectedVehicleId) void fetchRoute(selectedVehicleId, destination);
  }, [selectedVehicleId, fetchRoute]);

  useEffect(() => { void loadFleet(); }, []);
  useEffect(() => {
    // Freshness buckets resolve at 5s/30s/120s boundaries, so updating more often
    // only re-renders the dispatcher without changing any visible state.
    const timer = window.setInterval(() => setClock((value) => value + 1), 5000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    if (!selectedVehicleId && vehicles.length > 0) setSelectedVehicleId(vehicles[0].vehicle_id);
  }, [selectedVehicleId, vehicles]);

  useEffect(() => {
    setRouteMode(false);
    setRouteDestination(null);
    setRoutePlan(null);
    setRouteError('');
  }, [selectedVehicleId]);

  useEffect(() => {
    if (!routeDestination || !selectedVehicleId || !routePlan) return;
    const timer = window.setInterval(() => {
      void fetchRoute(selectedVehicleId, routeDestination, true);
    }, 20_000);
    return () => window.clearInterval(timer);
  }, [routeDestination, selectedVehicleId, routePlan, fetchRoute]);

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
      socket.onopen = () => { retryMs = 1000; setSocketUp(true); };
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
          // Transport failures are handled by reconnect logic.
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
      credentials: 'same-origin', signal: controller.signal
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
      .finally(() => { if (!controller.signal.aborted) setHistoryLoading(false); });

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

  const filteredVehicles = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return sortedVehicles.filter((vehicle) => {
      const state = freshness(vehicle);
      const matchesFilter = fleetFilter === 'ALL'
        || (fleetFilter === 'LIVE' && state === 'LIVE')
        || (fleetFilter === 'OFFLINE' && ['OFFLINE', 'NO DATA', 'STALE'].includes(state));
      const matchesQuery = !normalized
        || vehicle.vehicle_code.toLowerCase().includes(normalized)
        || vehicle.label.toLowerCase().includes(normalized);
      return matchesFilter && matchesQuery;
    });
  }, [sortedVehicles, fleetFilter, query]);

  function clearRoute() {
    setRouteMode(false);
    setRouteDestination(null);
    setRoutePlan(null);
    setRouteError('');
  }

  async function logout() {
    const csrf = sessionStorage.getItem('csrf_token') ?? '';
    await fetch('/api/v1/auth/logout', {
      method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf }
    }).catch(() => undefined);
    sessionStorage.removeItem('csrf_token');
    setAuthenticated(false);
    setVehicles([]);
  }

  if (authenticated === null) return <div className="boot">Connecting…</div>;
  if (!authenticated) return <Login onAuthenticated={() => void loadFleet()} />;

  return (
    <main className="dispatcher-shell">
      <h1 className="visually-hidden">EMS Tracker dispatcher console</h1>
      <section className="dispatcher-map" aria-label="Dispatcher live map">
        <MapView
          vehicles={filteredVehicles}
          selectedVehicleId={selectedVehicleId}
          history={historyOpen ? history : []}
          playbackIndex={historyOpen ? playbackIndex : -1}
          focusRequest={focusRequest}
          planningRoute={routeMode}
          routePlan={routePlan}
          routeDestination={routeDestination}
          onSelect={(id) => { setSelectedVehicleId(id); setHistoryOpen(false); }}
          onDestination={chooseRouteDestination}
        />
      </section>

      <aside className="fleet-rail">
        <div className="fleet-brand"><BrandMark /></div>
        <div className="rail-search">
          <span aria-hidden="true">⌕</span>
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search ambulances" aria-label="Search ambulances" />
        </div>
        <div className="rail-summary">
          <div><strong>{vehicles.length}</strong><span>Units</span></div>
          <div><strong>{liveCount}</strong><span>Live</span></div>
          <div><strong>{offlineCount}</strong><span>Offline</span></div>
        </div>
        <div className="rail-section-title">Fleet</div>
        <div className="vehicle-list">
          {filteredVehicles.map((vehicle) => {
            const state = freshness(vehicle);
            return (
              <button
                type="button"
                className={`vehicle-row${selectedVehicleId === vehicle.vehicle_id ? ' selected' : ''}`}
                key={vehicle.vehicle_id}
                onClick={() => { setSelectedVehicleId(vehicle.vehicle_id); setHistoryOpen(false); }}
              >
                <span className={`unit-badge ${statusClass(state)}`}><UnitIcon /></span>
                <span className="vehicle-copy">
                  <strong>{vehicle.vehicle_code}</strong>
                  <small>{vehicle.label && vehicle.label !== vehicle.vehicle_code ? vehicle.label : vehicle.status}</small>
                </span>
                <span className="vehicle-age">{ageLabel(vehicle.location?.recorded_at)}</span>
              </button>
            );
          })}
          {filteredVehicles.length === 0 && <div className="empty">No matching ambulances.</div>}
        </div>
        <div className="rail-footer">
          <span className={socketUp ? 'presence-dot live' : 'presence-dot'} />
          <div><strong>Dispatcher</strong><small>{socketUp ? 'Realtime connected' : 'Reconnecting…'}</small></div>
          <button type="button" onClick={() => void logout()}>Sign out</button>
        </div>
      </aside>

      <div className="map-top-controls" id="map-top-controls">
        <div className="filter-group" role="group" aria-label="Fleet filter">
          {(['ALL', 'LIVE', 'OFFLINE'] as FleetFilter[]).map((value) => (
            <button key={value} className={fleetFilter === value ? 'active' : ''} onClick={() => setFleetFilter(value)}>
              {value === 'ALL' ? 'All' : value === 'LIVE' ? 'Live' : 'Offline'}
            </button>
          ))}
        </div>
        <div className={socketUp ? 'realtime-pill live' : 'realtime-pill'}><span />{socketUp ? 'Live' : 'Reconnecting'}</div>
      </div>

      {routeMode && (
        <div className="route-pick-banner" role="status">
          <div><strong>Choose destination</strong><span>Click anywhere on the map to route {selectedVehicle?.vehicle_code ?? 'this ambulance'}.</span></div>
          <button type="button" onClick={() => setRouteMode(false)}>Cancel</button>
        </div>
      )}

      <aside className="unit-inspector">
        {selectedVehicle ? (
          <>
            <div className="inspector-head">
              <div>
                <h2>{selectedVehicle.vehicle_code}</h2>
                <p>{selectedVehicle.label && selectedVehicle.label !== selectedVehicle.vehicle_code ? selectedVehicle.label : 'Ambulance unit'}</p>
              </div>
              <span className={`state-pill ${statusClass(freshness(selectedVehicle))}`}>
                <i />{freshness(selectedVehicle) === 'LIVE' ? 'Online' : freshness(selectedVehicle)}
              </span>
            </div>

            <div className="status-card">
              <span className={`large-status-dot ${statusClass(freshness(selectedVehicle))}`} />
              <div><strong>{selectedVehicle.status || 'Active'}</strong><small>{freshness(selectedVehicle) === 'LIVE' ? 'Tracker reporting normally' : 'Tracker connection degraded'}</small></div>
            </div>

            <div className="metric-grid">
              <div><span>Speed</span><strong>{formatSpeed(selectedVehicle.location?.speed_mps)}</strong></div>
              <div><span>Battery</span><strong>{selectedVehicle.location?.battery_pct == null ? '—' : `${selectedVehicle.location.battery_pct}%`}</strong></div>
              <div><span>Network</span><strong>{selectedVehicle.location?.network_type || '—'}</strong></div>
              <div><span>GPS</span><strong>{selectedVehicle.location?.accuracy_m == null ? '—' : `±${Math.round(selectedVehicle.location.accuracy_m)} m`}</strong></div>
            </div>

            <div className="location-card">
              <div>
                <span>Current position</span>
                <strong>{formatCoordinates(selectedVehicle.location)}</strong>
                <small>Updated {ageLabel(selectedVehicle.location?.recorded_at)}</small>
              </div>
              <button onClick={() => setFocusRequest((value) => value + 1)}>View on map</button>
            </div>

            {(routeLoading || routePlan || routeError) && (
              <section className={`route-card${routePlan?.approximate ? ' approximate' : ''}`} aria-label="Route and ETA">
                <div className="route-card-head">
                  <div><span>Route to destination</span><strong>{routeLoading && !routePlan ? 'Calculating…' : routePlan ? formatETA(routePlan.duration_seconds) : 'Unavailable'}</strong></div>
                  {routePlan && <div className="route-distance">{formatDistance(routePlan.distance_m)}</div>}
                </div>
                {routePlan && (
                  <>
                    <div className="route-meta">
                      <span>ETA {new Date(routePlan.eta_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
                      <span>{routePlan.approximate ? 'Approximate' : 'Road route'}</span>
                    </div>
                    <p>{routePlan.approximate
                      ? 'No road router is configured or reachable, so this ETA uses live speed and straight-line distance with a road factor.'
                      : `Road route refreshed from the ambulance's latest persisted position · ${ageLabel(routePlan.location_recorded_at)}.`}</p>
                  </>
                )}
                {routeError && <p className="route-error">{routeError}</p>}
                <div className="route-card-actions">
                  {routeDestination && selectedVehicleId && (
                    <button type="button" onClick={() => void fetchRoute(selectedVehicleId, routeDestination)}>Refresh</button>
                  )}
                  <button type="button" onClick={clearRoute}>Clear</button>
                </div>
              </section>
            )}

            <dl className="detail-list">
              <div><dt>Heading</dt><dd>{formatBearing(selectedVehicle.location?.bearing_deg)}</dd></div>
              <div><dt>Connection</dt><dd>{selectedVehicle.connected ? 'WebSocket connected' : 'Not connected'}</dd></div>
              <div><dt>Session</dt><dd>{selectedVehicle.location?.tracking_session_id ? selectedVehicle.location.tracking_session_id.slice(0, 8) : '—'}</dd></div>
            </dl>

            <div className="inspector-actions inspector-actions-three">
              <button className="secondary-action" onClick={() => setHistoryOpen((value) => !value)}>{historyOpen ? 'Hide history' : 'History'}</button>
              <button
                className={routeMode ? 'primary-action' : 'secondary-action'}
                disabled={!selectedVehicle.location}
                onClick={() => { setHistoryOpen(false); setRouteMode((value) => !value); }}
              >{routeMode ? 'Cancel route' : 'Route'}</button>
              <button className="primary-action" onClick={() => setFocusRequest((value) => value + 1)}>Center</button>
            </div>
          </>
        ) : <div className="empty details-empty">Select an ambulance on the map.</div>}
      </aside>

      {historyOpen && selectedVehicle && (
        <section className="history-drawer">
          <div className="history-head">
            <div>
              <span>Route history · {selectedVehicle.vehicle_code}</span>
              <strong>{historyLoading ? 'Loading…' : history.length > 0 ? formatPlaybackTime(selectedPlaybackSample) : 'No route data'}</strong>
            </div>
            <div className="history-actions">
              <select value={historyHours} onChange={(event) => setHistoryHours(Number(event.target.value))} aria-label="History range">
                <option value={1}>1 hour</option>
                <option value={6}>6 hours</option>
                <option value={24}>24 hours</option>
              </select>
              <button disabled={history.length < 2} onClick={() => {
                if (playbackIndex >= history.length - 1) setPlaybackIndex(0);
                setPlaying((value) => !value);
              }}>{playing ? 'Pause' : 'Play'}</button>
              <button onClick={() => setHistoryOpen(false)}>Close</button>
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
          <div className="history-meta">
            <span>{history.length > 0 ? new Date(history[0].recorded_at).toLocaleTimeString() : '—'}</span>
            <span>{historyError || `${history.length.toLocaleString()} points`}</span>
            <span>{history.length > 0 ? new Date(history[history.length - 1].recorded_at).toLocaleTimeString() : '—'}</span>
          </div>
        </section>
      )}
    </main>
  );
}
