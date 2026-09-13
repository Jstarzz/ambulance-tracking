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

type User = { id: string; username: string; role: 'dispatcher' | 'admin' };
type FleetResponse = { vehicles: Vehicle[]; server_time: string };
type HistoryResponse = { vehicle_id: string; from: string; to: string; locations: LocationSample[] };
type SessionResponse = { user: User; mfa_enabled: boolean };
type LiveMessage = {
  type: string;
  location?: LocationSample;
  vehicle_id?: string;
  connected?: boolean;
  event?: VehicleEvent;
};

type VehicleEvent = {
  id: string;
  vehicle_id: string;
  vehicle_code?: string;
  device_id: string;
  tracking_session_id: string;
  event_type: string;
  severity: 'info' | 'warning' | 'critical';
  recorded_at: string;
  latitude?: number;
  longitude?: number;
  metadata?: Record<string, unknown>;
  acknowledged_at?: string;
};

type ETAResponse = {
  vehicle_id: string;
  distance_m: number;
  duration_seconds: number;
  eta_at: string;
  method: 'road_route' | 'kinematic_fallback';
  approximate: boolean;
  assumed_speed_kph?: number;
  destination: { latitude: number; longitude: number };
};

type Enrollment = {
  id: string;
  vehicle_code: string;
  vehicle_label: string;
  device_name: string;
  expires_at: string;
  created_at: string;
  consumed_at?: string;
};

type AdminDevice = {
  id: string;
  vehicle_id: string;
  vehicle_code: string;
  vehicle_label: string;
  name: string;
  active: boolean;
  created_at: string;
  last_seen_at?: string;
};

type APIToken = {
  id: string;
  name: string;
  scopes: string[];
  expires_at?: string;
  created_at: string;
  last_used_at?: string;
};

type Freshness = 'LIVE' | 'DELAYED' | 'STALE' | 'OFFLINE' | 'NO DATA';
type Destination = { latitude: number; longitude: number } | null;

const EMPTY_GEOJSON = { type: 'FeatureCollection' as const, features: [] };
const csrf = () => sessionStorage.getItem('csrf_token') ?? '';

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
  const hours = Math.floor(minutes / 60);
  return hours < 24 ? `${hours}h ago` : `${Math.floor(hours / 24)}d ago`;
}

function formatSpeed(speed?: number): string {
  return speed == null ? '—' : `${Math.round(speed * 3.6)} km/h`;
}

function formatBearing(bearing?: number): string {
  if (bearing == null) return '—';
  const normalized = ((bearing % 360) + 360) % 360;
  const points = ['N', 'NE', 'E', 'SE', 'S', 'SW', 'W', 'NW'];
  return `${points[Math.round(normalized / 45) % 8]} ${Math.round(normalized)}°`;
}

function formatCoordinates(location?: LocationSample): string {
  if (!location) return 'No position';
  return `${location.latitude.toFixed(5)}, ${location.longitude.toFixed(5)}`;
}

function formatDistance(meters: number): string {
  return meters < 1000 ? `${Math.round(meters)} m` : `${(meters / 1000).toFixed(meters < 10_000 ? 1 : 0)} km`;
}

function formatDuration(seconds: number): string {
  const minutes = Math.max(1, Math.round(seconds / 60));
  if (minutes < 60) return `${minutes} min`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

function MapView({
  vehicles,
  selectedVehicleId,
  history,
  playbackIndex,
  destination,
  etaTargetMode,
  onSelect,
  onTarget
}: {
  vehicles: Vehicle[];
  selectedVehicleId: string | null;
  history: LocationSample[];
  playbackIndex: number;
  destination: Destination;
  etaTargetMode: boolean;
  onSelect: (vehicleId: string) => void;
  onTarget: (destination: NonNullable<Destination>) => void;
}) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const mapRef = useRef<MapLibreMap | null>(null);
  const markersRef = useRef(new Map<string, Marker>());
  const animationFramesRef = useRef(new Map<string, number>());
  const destinationMarkerRef = useRef<Marker | null>(null);
  const previousSelectionRef = useRef<string | null>(null);
  const [styleReady, setStyleReady] = useState(false);

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;
    const map = new maplibregl.Map({
      container: containerRef.current,
      center: [-62.76, 17.29],
      zoom: 10.3,
      style: 'https://tiles.openfreemap.org/styles/dark',
      pitchWithRotate: false,
      dragRotate: false
    });
    map.addControl(new maplibregl.NavigationControl({ showCompass: false }), 'bottom-right');
    map.addControl(new maplibregl.AttributionControl({ compact: true }), 'bottom-right');
    map.on('load', () => {
      map.addSource('history-route', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addSource('history-progress', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addSource('playback-point', { type: 'geojson', data: EMPTY_GEOJSON });
      map.addLayer({ id: 'history-route', type: 'line', source: 'history-route', paint: { 'line-color': '#8a939d', 'line-width': 3, 'line-opacity': 0.58 } });
      map.addLayer({ id: 'history-progress', type: 'line', source: 'history-progress', paint: { 'line-color': '#4f9cf9', 'line-width': 5, 'line-opacity': 0.96 } });
      map.addLayer({
        id: 'playback-point',
        type: 'circle',
        source: 'playback-point',
        paint: { 'circle-radius': 8, 'circle-color': '#4f9cf9', 'circle-stroke-color': '#ffffff', 'circle-stroke-width': 3 }
      });
      setStyleReady(true);
    });
    mapRef.current = map;
    return () => {
      animationFramesRef.current.forEach((frame) => cancelAnimationFrame(frame));
      markersRef.current.forEach((marker) => marker.remove());
      destinationMarkerRef.current?.remove();
      map.remove();
      mapRef.current = null;
    };
  }, []);

  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    const handler = (event: maplibregl.MapMouseEvent) => {
      if (!etaTargetMode) return;
      onTarget({ latitude: event.lngLat.lat, longitude: event.lngLat.lng });
    };
    map.on('click', handler);
    map.getCanvas().style.cursor = etaTargetMode ? 'crosshair' : '';
    return () => {
      map.off('click', handler);
      map.getCanvas().style.cursor = '';
    };
  }, [etaTargetMode, onTarget]);

  const animateMarker = useCallback((id: string, marker: Marker, target: [number, number]) => {
    const existing = animationFramesRef.current.get(id);
    if (existing) cancelAnimationFrame(existing);
    const start = marker.getLngLat();
    const started = performance.now();
    const duration = 650;
    const tick = (now: number) => {
      const raw = Math.min(1, (now - started) / duration);
      const t = 1 - Math.pow(1 - raw, 3);
      marker.setLngLat([
        start.lng + (target[0] - start.lng) * t,
        start.lat + (target[1] - start.lat) * t
      ]);
      if (raw < 1) {
        animationFramesRef.current.set(id, requestAnimationFrame(tick));
      } else {
        animationFramesRef.current.delete(id);
      }
    };
    animationFramesRef.current.set(id, requestAnimationFrame(tick));
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
        const pulse = document.createElement('span');
        pulse.className = 'marker-pulse';
        const core = document.createElement('span');
        core.className = 'marker-core';
        core.innerHTML = '<span class="marker-arrow">▲</span>';
        const label = document.createElement('span');
        label.className = 'marker-label';
        element.append(pulse, core, label);
        element.addEventListener('click', (event) => {
          event.stopPropagation();
          onSelect(vehicle.vehicle_id);
        });
        marker = new maplibregl.Marker({ element, anchor: 'center' })
          .setLngLat([vehicle.location.longitude, vehicle.location.latitude])
          .addTo(map);
        markersRef.current.set(vehicle.vehicle_id, marker);
      } else {
        animateMarker(vehicle.vehicle_id, marker, [vehicle.location.longitude, vehicle.location.latitude]);
      }
      const element = marker.getElement();
      element.className = `vehicle-marker ${state}${selectedVehicleId === vehicle.vehicle_id ? ' selected' : ''}`;
      const label = element.querySelector('.marker-label');
      if (label) label.textContent = vehicle.vehicle_code;
      const arrow = element.querySelector('.marker-arrow') as HTMLElement | null;
      if (arrow) arrow.style.transform = `rotate(${vehicle.location.bearing_deg ?? 0}deg)`;
    }
    for (const [id, marker] of markersRef.current.entries()) {
      if (!active.has(id)) {
        marker.remove();
        markersRef.current.delete(id);
      }
    }
  }, [animateMarker, onSelect, selectedVehicleId, vehicles]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map || !destination) {
      destinationMarkerRef.current?.remove();
      destinationMarkerRef.current = null;
      return;
    }
    if (!destinationMarkerRef.current) {
      const el = document.createElement('div');
      el.className = 'destination-marker';
      el.innerHTML = '<span></span>';
      destinationMarkerRef.current = new maplibregl.Marker({ element: el, anchor: 'bottom' }).addTo(map);
    }
    destinationMarkerRef.current.setLngLat([destination.longitude, destination.latitude]);
  }, [destination]);

  useEffect(() => {
    if (!styleReady) return;
    const map = mapRef.current;
    if (!map) return;
    const coordinates = history.map((sample) => [sample.longitude, sample.latitude]);
    const progressCoordinates = playbackIndex >= 0 ? coordinates.slice(0, playbackIndex + 1) : [];
    const selectedSample = playbackIndex >= 0 ? history[playbackIndex] : undefined;
    (map.getSource('history-route') as GeoJSONSource | undefined)?.setData(coordinates.length > 1 ? {
      type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates }
    } : EMPTY_GEOJSON);
    (map.getSource('history-progress') as GeoJSONSource | undefined)?.setData(progressCoordinates.length > 1 ? {
      type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: progressCoordinates }
    } : EMPTY_GEOJSON);
    (map.getSource('playback-point') as GeoJSONSource | undefined)?.setData(selectedSample ? {
      type: 'Feature', properties: {}, geometry: { type: 'Point', coordinates: [selectedSample.longitude, selectedSample.latitude] }
    } : EMPTY_GEOJSON);
  }, [history, playbackIndex, styleReady]);

  useEffect(() => {
    if (!styleReady || history.length < 2) return;
    const map = mapRef.current;
    if (!map) return;
    const bounds = new maplibregl.LngLatBounds();
    history.forEach((sample) => bounds.extend([sample.longitude, sample.latitude]));
    map.fitBounds(bounds, { padding: { top: 110, right: 90, bottom: 210, left: 390 }, duration: 650, maxZoom: 15 });
  }, [history, styleReady]);

  useEffect(() => {
    if (previousSelectionRef.current === selectedVehicleId) return;
    previousSelectionRef.current = selectedVehicleId;
    if (!selectedVehicleId) return;
    const selected = vehicles.find((vehicle) => vehicle.vehicle_id === selectedVehicleId);
    if (!selected?.location) return;
    mapRef.current?.easeTo({
      center: [selected.location.longitude, selected.location.latitude],
      zoom: Math.max(mapRef.current.getZoom(), 13),
      offset: [90, -20],
      duration: 650,
      essential: true
    });
  }, [selectedVehicleId, vehicles]);

  return <div className="map-canvas" ref={containerRef} aria-label="Live ambulance map" />;
}

function Login({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [challenge, setChallenge] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  async function submitCredentials(event: FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      const response = await fetch('/api/v1/auth/login', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin', body: JSON.stringify({ username, password })
      });
      if (!response.ok) {
        setError(response.status === 429 ? 'Too many attempts. Try again shortly.' : 'Invalid username or password.');
        return;
      }
      const body = await response.json() as { csrf_token?: string; mfa_required?: boolean; challenge_token?: string };
      if (body.mfa_required && body.challenge_token) {
        setChallenge(body.challenge_token);
        return;
      }
      if (body.csrf_token) sessionStorage.setItem('csrf_token', body.csrf_token);
      onAuthenticated();
    } catch {
      setError('Tracking server unavailable.');
    } finally {
      setSubmitting(false);
    }
  }

  async function submitMFA(event: FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      const response = await fetch('/api/v1/auth/mfa/verify', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin', body: JSON.stringify({ challenge_token: challenge, code })
      });
      if (!response.ok) {
        setError('That authenticator code is invalid or expired.');
        return;
      }
      const body = await response.json() as { csrf_token: string };
      sessionStorage.setItem('csrf_token', body.csrf_token);
      onAuthenticated();
    } catch {
      setError('Tracking server unavailable.');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="login-page">
      <div className="login-brand">
        <span className="ems-symbol">✚</span>
        <div><strong>EMS Tracker</strong><span>Saint Kitts &amp; Nevis</span></div>
      </div>
      {!challenge ? (
        <form className="login-card" onSubmit={submitCredentials}>
          <div><h1>Dispatcher</h1><p>Sign in to live fleet operations.</p></div>
          <label>Username<input value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" required autoFocus /></label>
          <label>Password<input type="password" value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" required /></label>
          {error && <div className="form-error" role="alert">{error}</div>}
          <button className="google-primary" disabled={submitting}>{submitting ? 'Signing in…' : 'Sign in'}</button>
        </form>
      ) : (
        <form className="login-card" onSubmit={submitMFA}>
          <button className="text-button back-button" type="button" onClick={() => { setChallenge(''); setCode(''); setError(''); }}>← Back</button>
          <div><h1>Verification</h1><p>Enter the 6-digit code from your authenticator app.</p></div>
          <label>Authenticator code<input className="otp-input" inputMode="numeric" pattern="[0-9]{6}" maxLength={6} value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, ''))} autoComplete="one-time-code" required autoFocus /></label>
          {error && <div className="form-error" role="alert">{error}</div>}
          <button className="google-primary" disabled={submitting || code.length !== 6}>{submitting ? 'Verifying…' : 'Verify'}</button>
        </form>
      )}
    </main>
  );
}

function AdminPanel({ onClose }: { onClose: () => void }) {
  const [tab, setTab] = useState<'enroll' | 'devices' | 'ai' | 'security'>('enroll');
  const [vehicleCode, setVehicleCode] = useState('');
  const [vehicleLabel, setVehicleLabel] = useState('');
  const [enrollmentCode, setEnrollmentCode] = useState('');
  const [enrollmentExpiry, setEnrollmentExpiry] = useState('');
  const [devices, setDevices] = useState<AdminDevice[]>([]);
  const [tokens, setTokens] = useState<APIToken[]>([]);
  const [newToken, setNewToken] = useState('');
  const [mfaSecret, setMfaSecret] = useState('');
  const [mfaURI, setMfaURI] = useState('');
  const [mfaCode, setMfaCode] = useState('');
  const [message, setMessage] = useState('');

  const loadAdminData = useCallback(async () => {
    const [deviceResp, tokenResp] = await Promise.all([
      fetch('/api/v1/admin/devices', { credentials: 'same-origin' }),
      fetch('/api/v1/admin/api-tokens', { credentials: 'same-origin' })
    ]);
    if (deviceResp.ok) setDevices(((await deviceResp.json()) as { devices: AdminDevice[] }).devices);
    if (tokenResp.ok) setTokens(((await tokenResp.json()) as { tokens: APIToken[] }).tokens);
  }, []);

  useEffect(() => { void loadAdminData(); }, [loadAdminData]);

  async function createEnrollment(event: FormEvent) {
    event.preventDefault();
    setMessage('');
    const response = await fetch('/api/v1/admin/enrollments', {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ vehicle_code: vehicleCode, vehicle_label: vehicleLabel || vehicleCode, expires_minutes: 30 })
    });
    if (!response.ok) { setMessage('Could not create enrollment. Check that vehicle code is valid and unused.'); return; }
    const body = await response.json() as { enrollment_code: string; enrollment: Enrollment };
    setEnrollmentCode(body.enrollment_code);
    setEnrollmentExpiry(body.enrollment.expires_at);
    setMessage('Give this one-time code to the ambulance phone. It disappears after use.');
    void loadAdminData();
  }

  async function revokeDevice(deviceID: string) {
    if (!window.confirm('Revoke this tracker? Its active sessions will stop working.')) return;
    const response = await fetch(`/api/v1/admin/devices/${encodeURIComponent(deviceID)}/revoke`, {
      method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf() }
    });
    if (response.ok) void loadAdminData();
  }

  async function createAIToken(event: FormEvent) {
    event.preventDefault();
    setNewToken('');
    const response = await fetch('/api/v1/admin/api-tokens', {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() },
      body: JSON.stringify({ name: `AI assistant ${new Date().toLocaleDateString()}`, scopes: ['fleet:read', 'events:read'], expires_days: 90 })
    });
    if (!response.ok) { setMessage('Could not create AI access token.'); return; }
    const body = await response.json() as { access_token: string };
    setNewToken(body.access_token);
    setMessage('Copy this token now. The server only stores its hash.');
    void loadAdminData();
  }

  async function beginMFA() {
    const response = await fetch('/api/v1/auth/mfa/setup', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf() } });
    if (!response.ok) { setMessage('MFA setup is unavailable. The server encryption key may not be configured yet.'); return; }
    const body = await response.json() as { secret: string; otpauth_uri: string };
    setMfaSecret(body.secret);
    setMfaURI(body.otpauth_uri);
    setMessage('Add this account to an authenticator app, then verify one code below.');
  }

  async function confirmMFA(event: FormEvent) {
    event.preventDefault();
    const response = await fetch('/api/v1/auth/mfa/confirm', {
      method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf() }, body: JSON.stringify({ code: mfaCode })
    });
    setMessage(response.ok ? 'TOTP MFA is enabled for this account.' : 'Invalid code. Check the authenticator clock and try again.');
    if (response.ok) { setMfaSecret(''); setMfaURI(''); setMfaCode(''); }
  }

  return (
    <div className="sheet-backdrop" onMouseDown={(event) => { if (event.currentTarget === event.target) onClose(); }}>
      <section className="admin-sheet" aria-label="Administration">
        <header className="sheet-header"><div><span className="eyebrow">ADMINISTRATION</span><h2>Fleet settings</h2></div><button className="icon-button" onClick={onClose} aria-label="Close">×</button></header>
        <nav className="sheet-tabs">
          <button className={tab === 'enroll' ? 'active' : ''} onClick={() => setTab('enroll')}>Add ambulance</button>
          <button className={tab === 'devices' ? 'active' : ''} onClick={() => setTab('devices')}>Devices</button>
          <button className={tab === 'ai' ? 'active' : ''} onClick={() => setTab('ai')}>AI access</button>
          <button className={tab === 'security' ? 'active' : ''} onClick={() => setTab('security')}>Security</button>
        </nav>
        <div className="sheet-content">
          {tab === 'enroll' && <>
            <div className="sheet-intro"><h3>Register a tracker in under a minute</h3><p>Create a short-lived one-time code here. On the phone, install the app and type only that code; the server URL and long device credential stay out of the normal setup flow.</p></div>
            <form className="stack-form" onSubmit={createEnrollment}>
              <label>Vehicle code<input placeholder="AMB-02" value={vehicleCode} onChange={(event) => setVehicleCode(event.target.value.toUpperCase())} required /></label>
              <label>Display name<input placeholder="Ambulance 02" value={vehicleLabel} onChange={(event) => setVehicleLabel(event.target.value)} /></label>
              <button className="google-primary">Create 30-minute enrollment code</button>
            </form>
            {enrollmentCode && <div className="enrollment-result"><span>ONE-TIME CODE</span><strong>{enrollmentCode}</strong><small>Expires {new Date(enrollmentExpiry).toLocaleTimeString()}</small><button onClick={() => void navigator.clipboard.writeText(enrollmentCode)}>Copy code</button></div>}
          </>}
          {tab === 'devices' && <div className="admin-list">
            {devices.map((device) => <div className="admin-row" key={device.id}><div><strong>{device.vehicle_code}</strong><span>{device.name} · {device.active ? 'Active' : 'Revoked'} · {device.last_seen_at ? `seen ${ageLabel(device.last_seen_at)}` : 'never seen'}</span></div>{device.active && <button className="danger-text" onClick={() => void revokeDevice(device.id)}>Revoke</button>}</div>)}
            {devices.length === 0 && <p className="muted">No tracker devices yet.</p>}
          </div>}
          {tab === 'ai' && <>
            <div className="sheet-intro"><h3>Read-only operational AI access</h3><p>Create a scoped token for a local AI service or automation. It can read fleet telemetry and alerts; it cannot provision devices, acknowledge alerts, change credentials, or control ambulances.</p></div>
            <form onSubmit={createAIToken}><button className="google-primary">Create 90-day AI token</button></form>
            {newToken && <div className="token-result"><span>SHOWS ONCE</span><code>{newToken}</code><button onClick={() => void navigator.clipboard.writeText(newToken)}>Copy token</button></div>}
            <div className="endpoint-box"><code>GET /api/v1/ai/fleet-context</code><code>GET /api/v1/ai/vehicles/:id/context</code></div>
            <div className="admin-list">{tokens.map((token) => <div className="admin-row" key={token.id}><div><strong>{token.name}</strong><span>{token.scopes.join(', ')} · {token.last_used_at ? `used ${ageLabel(token.last_used_at)}` : 'never used'}</span></div></div>)}</div>
          </>}
          {tab === 'security' && <>
            <div className="sheet-intro"><h3>Authenticator-app MFA</h3><p>TOTP adds a second factor after the dispatcher password. The TOTP seed is encrypted at rest with a deployment-level AES-256-GCM key.</p></div>
            {!mfaSecret ? <button className="google-primary" onClick={() => void beginMFA()}>Set up authenticator MFA</button> : <form className="stack-form" onSubmit={confirmMFA}>
              <div className="secret-box"><span>SECRET</span><code>{mfaSecret}</code><a href={mfaURI}>Open authenticator app</a></div>
              <label>6-digit code<input className="otp-input" inputMode="numeric" maxLength={6} value={mfaCode} onChange={(event) => setMfaCode(event.target.value.replace(/\D/g, ''))} required /></label>
              <button className="google-primary" disabled={mfaCode.length !== 6}>Verify and enable MFA</button>
            </form>}
          </>}
          {message && <div className="inline-message">{message}</div>}
        </div>
      </section>
    </div>
  );
}

export default function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null);
  const [session, setSession] = useState<SessionResponse | null>(null);
  const [vehicles, setVehicles] = useState<Vehicle[]>([]);
  const [socketUp, setSocketUp] = useState(false);
  const [selectedVehicleId, setSelectedVehicleId] = useState<string | null>(null);
  const [history, setHistory] = useState<LocationSample[]>([]);
  const [historyHours, setHistoryHours] = useState(1);
  const [playbackIndex, setPlaybackIndex] = useState(-1);
  const [playing, setPlaying] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [query, setQuery] = useState('');
  const [fleetOpen, setFleetOpen] = useState(true);
  const [adminOpen, setAdminOpen] = useState(false);
  const [events, setEvents] = useState<VehicleEvent[]>([]);
  const [destination, setDestination] = useState<Destination>(null);
  const [etaTargetMode, setEtaTargetMode] = useState(false);
  const [eta, setEta] = useState<ETAResponse | null>(null);
  const [etaLoading, setEtaLoading] = useState(false);
  const [, setClock] = useState(0);

  const loadSession = useCallback(async () => {
    const response = await fetch('/api/v1/auth/session', { credentials: 'same-origin' });
    if (!response.ok) { setSession(null); return null; }
    const body = await response.json() as SessionResponse;
    setSession(body);
    return body;
  }, []);

  const loadFleet = useCallback(async () => {
    try {
      const response = await fetch('/api/v1/vehicles', { credentials: 'same-origin' });
      if (response.status === 401) { setAuthenticated(false); setVehicles([]); setSession(null); return; }
      if (!response.ok) throw new Error('fleet request failed');
      const body = await response.json() as FleetResponse;
      setVehicles(body.vehicles.map((vehicle) => ({ ...vehicle, connected: freshnessFromTime(vehicle.location?.recorded_at) === 'LIVE' })));
      setAuthenticated(true);
      void loadSession();
    } catch {
      setAuthenticated(false);
    }
  }, [loadSession]);

  const loadEvents = useCallback(async () => {
    const response = await fetch('/api/v1/events?hours=24', { credentials: 'same-origin' }).catch(() => null);
    if (!response?.ok) return;
    const body = await response.json() as { events: VehicleEvent[] };
    setEvents(body.events);
  }, []);

  useEffect(() => { void loadFleet(); }, [loadFleet]);
  useEffect(() => {
    if (!authenticated) return;
    void loadEvents();
    const id = window.setInterval(() => void loadEvents(), 20_000);
    return () => window.clearInterval(id);
  }, [authenticated, loadEvents]);
  useEffect(() => { const id = window.setInterval(() => setClock((v) => v + 1), 1000); return () => window.clearInterval(id); }, []);
  useEffect(() => { if (!selectedVehicleId && vehicles.length > 0) setSelectedVehicleId(vehicles[0].vehicle_id); }, [selectedVehicleId, vehicles]);

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
            setVehicles((current) => current.map((vehicle) => vehicle.vehicle_id === message.vehicle_id ? { ...vehicle, connected: Boolean(message.connected) } : vehicle));
          } else if (message.type === 'location' && message.location?.vehicle_id) {
            const sample = message.location;
            setVehicles((current) => current.map((vehicle) => vehicle.vehicle_id === sample.vehicle_id ? { ...vehicle, connected: true, location: sample } : vehicle));
          } else if (message.type === 'vehicle_event' && message.event) {
            setEvents((current) => [message.event!, ...current.filter((item) => item.id !== message.event!.id)]);
          }
        } catch { /* malformed frames do not tear down realtime */ }
      };
      socket.onclose = () => {
        setSocketUp(false);
        setVehicles((current) => current.map((vehicle) => ({ ...vehicle, connected: false })));
        if (!stopped) { retryTimer = window.setTimeout(connect, retryMs); retryMs = Math.min(retryMs * 2, 10_000); }
      };
      socket.onerror = () => socket?.close();
    };
    connect();
    return () => { stopped = true; setSocketUp(false); if (retryTimer) window.clearTimeout(retryTimer); socket?.close(); };
  }, [authenticated]);

  useEffect(() => {
    if (!authenticated || !selectedVehicleId) { setHistory([]); setPlaybackIndex(-1); return; }
    const controller = new AbortController();
    setHistoryLoading(true);
    setPlaying(false);
    fetch(`/api/v1/vehicles/${encodeURIComponent(selectedVehicleId)}/history?hours=${historyHours}`, { credentials: 'same-origin', signal: controller.signal })
      .then(async (response) => { if (!response.ok) throw new Error('history unavailable'); return await response.json() as HistoryResponse; })
      .then((body) => { setHistory(body.locations); setPlaybackIndex(body.locations.length - 1); })
      .catch(() => { if (!controller.signal.aborted) { setHistory([]); setPlaybackIndex(-1); } })
      .finally(() => { if (!controller.signal.aborted) setHistoryLoading(false); });
    return () => controller.abort();
  }, [authenticated, historyHours, selectedVehicleId]);

  useEffect(() => {
    if (!playing || history.length < 2) return;
    const timer = window.setInterval(() => {
      setPlaybackIndex((current) => {
        const step = Math.max(1, Math.ceil(history.length / 320));
        const next = Math.min(history.length - 1, current + step);
        if (next >= history.length - 1) setPlaying(false);
        return next;
      });
    }, 180);
    return () => window.clearInterval(timer);
  }, [history.length, playing]);

  const selectETATarget = useCallback((target: NonNullable<Destination>) => {
    setDestination(target);
    setEtaTargetMode(false);
  }, []);

  useEffect(() => {
    if (!selectedVehicleId || !destination) { setEta(null); return; }
    const controller = new AbortController();
    setEtaLoading(true);
    fetch(`/api/v1/vehicles/${encodeURIComponent(selectedVehicleId)}/eta?lat=${destination.latitude}&lon=${destination.longitude}`, { credentials: 'same-origin', signal: controller.signal })
      .then(async (response) => { if (!response.ok) throw new Error('ETA unavailable'); return await response.json() as ETAResponse; })
      .then(setEta)
      .catch(() => { if (!controller.signal.aborted) setEta(null); })
      .finally(() => { if (!controller.signal.aborted) setEtaLoading(false); });
    return () => controller.abort();
  }, [destination, selectedVehicleId]);

  const sortedVehicles = useMemo(() => [...vehicles].sort((a, b) => a.vehicle_code.localeCompare(b.vehicle_code)), [vehicles]);
  const filteredVehicles = useMemo(() => sortedVehicles.filter((vehicle) => `${vehicle.vehicle_code} ${vehicle.label}`.toLowerCase().includes(query.toLowerCase())), [query, sortedVehicles]);
  const selectedVehicle = useMemo(() => vehicles.find((vehicle) => vehicle.vehicle_id === selectedVehicleId) ?? null, [selectedVehicleId, vehicles]);
  const selectedPlaybackSample = playbackIndex >= 0 ? history[playbackIndex] : undefined;
  const liveCount = vehicles.filter((vehicle) => freshness(vehicle) === 'LIVE').length;
  const unackedEvents = events.filter((event) => !event.acknowledged_at);
  const activeAlert = unackedEvents[0];

  async function acknowledgeAlert(eventID: string) {
    const response = await fetch(`/api/v1/events/${encodeURIComponent(eventID)}/ack`, { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf() } });
    if (response.ok) setEvents((current) => current.map((event) => event.id === eventID ? { ...event, acknowledged_at: new Date().toISOString() } : event));
  }

  async function logout() {
    await fetch('/api/v1/auth/logout', { method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': csrf() } }).catch(() => undefined);
    sessionStorage.removeItem('csrf_token');
    setAuthenticated(false); setVehicles([]); setSession(null);
  }

  if (authenticated === null) return <div className="boot-screen"><div className="boot-spinner" /><span>Loading fleet</span></div>;
  if (!authenticated) return <Login onAuthenticated={() => void loadFleet()} />;

  return (
    <main className="map-app">
      <MapView
        vehicles={vehicles}
        selectedVehicleId={selectedVehicleId}
        history={history}
        playbackIndex={playbackIndex}
        destination={destination}
        etaTargetMode={etaTargetMode}
        onSelect={setSelectedVehicleId}
        onTarget={selectETATarget}
      />

      <header className="floating-topbar">
        <button className="round-icon" onClick={() => setFleetOpen((v) => !v)} aria-label="Toggle fleet">☰</button>
        <div className="map-search">
          <span className="search-icon">⌕</span>
          <input placeholder="Search ambulance or unit" value={query} onChange={(event) => { setQuery(event.target.value); setFleetOpen(true); }} />
          {query && <button onClick={() => setQuery('')} aria-label="Clear search">×</button>}
        </div>
        <div className={`realtime-pill ${socketUp ? 'online' : ''}`}><i />{socketUp ? `${liveCount}/${vehicles.length} live` : 'Reconnecting'}</div>
        {session?.user.role === 'admin' && <button className="top-action" onClick={() => setAdminOpen(true)}>Admin</button>}
        <button className="avatar-button" title={`${session?.user.username ?? 'Dispatcher'} · Sign out`} onClick={() => void logout()}>{(session?.user.username ?? 'D').slice(0, 1).toUpperCase()}</button>
      </header>

      {activeAlert && <div className={`incident-banner ${activeAlert.severity}`}>
        <span className="incident-icon">!</span>
        <div><strong>Possible crash · {activeAlert.vehicle_code ?? 'Vehicle'}</strong><span>Phone motion sensors detected a high-impact event. Verify by radio/phone before treating this as a confirmed crash.</span></div>
        <button onClick={() => { const v = vehicles.find((item) => item.vehicle_id === activeAlert.vehicle_id); if (v) setSelectedVehicleId(v.vehicle_id); }}>Locate</button>
        <button onClick={() => void acknowledgeAlert(activeAlert.id)}>Acknowledge</button>
      </div>}

      <aside className={`fleet-drawer ${fleetOpen ? 'open' : ''}`}>
        <div className="drawer-head"><div><span className="eyebrow">FLEET</span><strong>Ambulances</strong></div><span>{liveCount} live</span></div>
        <div className="vehicle-list">
          {filteredVehicles.map((vehicle) => {
            const state = freshness(vehicle);
            return <button className={`vehicle-card ${selectedVehicleId === vehicle.vehicle_id ? 'selected' : ''}`} key={vehicle.vehicle_id} onClick={() => { setSelectedVehicleId(vehicle.vehicle_id); setFleetOpen(window.innerWidth > 720); }}>
              <span className={`status-dot ${state.toLowerCase().replace(' ', '-')}`} />
              <div className="vehicle-card-main"><strong>{vehicle.vehicle_code}</strong><span>{vehicle.label || vehicle.status}</span></div>
              <div className="vehicle-card-side"><strong>{formatSpeed(vehicle.location?.speed_mps)}</strong><span>{ageLabel(vehicle.location?.recorded_at)}</span></div>
            </button>;
          })}
          {filteredVehicles.length === 0 && <div className="empty-state">No matching units.</div>}
        </div>
      </aside>

      <div className="map-tools">
        <button className={etaTargetMode ? 'active' : ''} onClick={() => setEtaTargetMode((v) => !v)} title="Click the map to estimate arrival time">◎ <span>ETA target</span></button>
        {destination && <button onClick={() => { setDestination(null); setEta(null); }}>× <span>Clear ETA</span></button>}
      </div>

      {etaTargetMode && <div className="map-hint">Click anywhere on the map to estimate arrival time for the selected ambulance</div>}

      {selectedVehicle && <section className="unit-sheet">
        <div className="unit-summary">
          <div className="unit-identity"><span className={`status-dot large ${freshness(selectedVehicle).toLowerCase().replace(' ', '-')}`} /><div><span className="eyebrow">{freshness(selectedVehicle)}</span><h2>{selectedVehicle.vehicle_code}</h2><p>{selectedVehicle.label}</p></div></div>
          <div className="quick-stat"><span>Speed</span><strong>{formatSpeed(selectedVehicle.location?.speed_mps)}</strong></div>
          <div className="quick-stat"><span>Heading</span><strong>{formatBearing(selectedVehicle.location?.bearing_deg)}</strong></div>
          <div className="quick-stat"><span>Battery</span><strong>{selectedVehicle.location?.battery_pct == null ? '—' : `${selectedVehicle.location.battery_pct}%`}</strong></div>
          <div className="quick-stat desktop-only"><span>Network</span><strong>{selectedVehicle.location?.network_type || '—'}</strong></div>
          <div className="quick-stat desktop-only"><span>Accuracy</span><strong>{selectedVehicle.location?.accuracy_m == null ? '—' : `±${Math.round(selectedVehicle.location.accuracy_m)} m`}</strong></div>
          {destination && <div className="eta-card"><span>{eta?.approximate ? 'Approx. ETA' : 'Road ETA'}</span>{etaLoading ? <strong>Calculating…</strong> : eta ? <><strong>{formatDuration(eta.duration_seconds)}</strong><small>{formatDistance(eta.distance_m)} · {new Date(eta.eta_at).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })}</small></> : <strong>Unavailable</strong>}</div>}
        </div>

        <div className="playback-strip">
          <button className="play-circle" disabled={history.length < 2} onClick={() => { if (playbackIndex >= history.length - 1) setPlaybackIndex(0); setPlaying((v) => !v); }}>{playing ? 'Ⅱ' : '▶'}</button>
          <div className="timeline-wrap"><input type="range" min={0} max={Math.max(0, history.length - 1)} value={Math.max(0, playbackIndex)} disabled={history.length < 2} onChange={(event) => { setPlaying(false); setPlaybackIndex(Number(event.target.value)); }} /><div><span>{historyLoading ? 'Loading route…' : selectedPlaybackSample ? new Date(selectedPlaybackSample.recorded_at).toLocaleTimeString() : formatCoordinates(selectedVehicle.location)}</span><span>{history.length.toLocaleString()} points</span></div></div>
          <select value={historyHours} onChange={(event) => setHistoryHours(Number(event.target.value))}><option value={1}>1 hour</option><option value={6}>6 hours</option><option value={24}>24 hours</option></select>
          <button className="live-button" onClick={() => { setPlaying(false); setPlaybackIndex(history.length - 1); }}>Live</button>
        </div>
      </section>}

      {adminOpen && <AdminPanel onClose={() => setAdminOpen(false)} />}
    </main>
  );
}
