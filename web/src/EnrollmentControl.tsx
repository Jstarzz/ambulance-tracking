import { useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';

type Vehicle = {
  vehicle_id: string;
  vehicle_code: string;
  label: string;
};

type FleetResponse = { vehicles: Vehicle[] };
type EnrollmentResponse = {
  vehicle_id: string;
  code: string;
  expires_at: string;
};
type CreateVehicleResponse = { vehicle?: Vehicle; error?: string };

export default function EnrollmentControl() {
  const [vehicles, setVehicles] = useState<Vehicle[]>([]);
  const [available, setAvailable] = useState(false);
  const [open, setOpen] = useState(false);
  const [vehicleId, setVehicleId] = useState('');
  const [enrollment, setEnrollment] = useState<EnrollmentResponse | null>(null);
  const [addingVehicle, setAddingVehicle] = useState(false);
  const [newVehicleCode, setNewVehicleCode] = useState('');
  const [newVehicleLabel, setNewVehicleLabel] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [portalHost, setPortalHost] = useState<HTMLElement | null>(null);

  useEffect(() => {
    if (portalHost) return;
    const existing = document.getElementById('map-top-controls');
    if (existing) {
      setPortalHost(existing);
      return;
    }
    const observer = new MutationObserver(() => {
      const el = document.getElementById('map-top-controls');
      if (el) {
        setPortalHost(el);
        observer.disconnect();
      }
    });
    observer.observe(document.body, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [portalHost]);

  useEffect(() => {
    let stopped = false;
    let timer: number | undefined;

    const refresh = async () => {
      try {
        const response = await fetch('/api/v1/vehicles', { credentials: 'same-origin' });
        if (!response.ok) {
          if (!stopped) setAvailable(false);
          return;
        }
        const body = (await response.json()) as FleetResponse;
        if (stopped) return;
        setVehicles(body.vehicles);
        setAvailable(true);
        setVehicleId((current) => current || body.vehicles[0]?.vehicle_id || '');
      } catch {
        if (!stopped) setAvailable(false);
      }
    };

    void refresh();
    timer = window.setInterval(() => void refresh(), 5000);
    return () => {
      stopped = true;
      if (timer) window.clearInterval(timer);
    };
  }, []);

  const selectedVehicle = useMemo(
    () => vehicles.find((vehicle) => vehicle.vehicle_id === vehicleId) ?? null,
    [vehicles, vehicleId]
  );

  if (!available) return null;

  async function requestEnrollment(targetVehicleId: string): Promise<EnrollmentResponse> {
    const csrf = sessionStorage.getItem('csrf_token') ?? '';
    const response = await fetch(`/api/v1/vehicles/${encodeURIComponent(targetVehicleId)}/enrollment`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'X-CSRF-Token': csrf }
    });
    const body = await response.json().catch(() => ({})) as Partial<EnrollmentResponse> & { error?: string };
    if (!response.ok || !body.code || !body.expires_at || !body.vehicle_id) {
      throw new Error(body.error || 'Could not create registration code.');
    }
    return body as EnrollmentResponse;
  }

  async function createCode() {
    if (!vehicleId || busy) return;
    setBusy(true);
    setError('');
    setEnrollment(null);
    try {
      setEnrollment(await requestEnrollment(vehicleId));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not create registration code.');
    } finally {
      setBusy(false);
    }
  }

  async function createVehicleAndCode() {
    if (busy) return;
    const code = newVehicleCode.trim().toUpperCase();
    const label = newVehicleLabel.trim();
    if (code.length < 2) {
      setError('Enter an ambulance code such as AMB-03.');
      return;
    }

    setBusy(true);
    setError('');
    setEnrollment(null);
    try {
      const csrf = sessionStorage.getItem('csrf_token') ?? '';
      const response = await fetch('/api/v1/vehicles', {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': csrf
        },
        body: JSON.stringify({ code, label })
      });
      const body = await response.json().catch(() => ({})) as CreateVehicleResponse;
      if (!response.ok || !body.vehicle) {
        throw new Error(body.error || 'Could not create ambulance.');
      }

      const created = body.vehicle;
      setVehicles((current) => [...current.filter((vehicle) => vehicle.vehicle_id !== created.vehicle_id), created]
        .sort((a, b) => a.vehicle_code.localeCompare(b.vehicle_code)));
      setVehicleId(created.vehicle_id);
      setNewVehicleCode('');
      setNewVehicleLabel('');
      setAddingVehicle(false);
      setEnrollment(await requestEnrollment(created.vehicle_id));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not create ambulance.');
    } finally {
      setBusy(false);
    }
  }

  function close() {
    setOpen(false);
    setEnrollment(null);
    setAddingVehicle(false);
    setNewVehicleCode('');
    setNewVehicleLabel('');
    setError('');
  }

  return (
    <>
      {portalHost && createPortal(
        <button className="tracker-enroll-launch" type="button" onClick={() => setOpen(true)}>
          Register tracker
        </button>,
        portalHost
      )}

      {open && (
        <div className="tracker-enroll-backdrop" role="presentation" onMouseDown={(event) => {
          if (event.target === event.currentTarget) close();
        }}>
          <section className="tracker-enroll-dialog" role="dialog" aria-modal="true" aria-labelledby="tracker-enroll-title">
            <div className="tracker-enroll-head">
              <div>
                <h2 id="tracker-enroll-title">Register tracker</h2>
                <p>Create an ambulance if needed, then connect its phone without touching SQL.</p>
              </div>
              <button className="tracker-enroll-close" type="button" onClick={close} aria-label="Close">×</button>
            </div>

            {!enrollment ? (
              addingVehicle ? (
                <>
                  <div className="tracker-enroll-form-grid">
                    <label className="tracker-enroll-field">
                      Ambulance code
                      <input
                        autoFocus
                        value={newVehicleCode}
                        onChange={(event) => setNewVehicleCode(event.target.value.toUpperCase())}
                        placeholder="AMB-03"
                        maxLength={32}
                      />
                    </label>
                    <label className="tracker-enroll-field">
                      Display name <span className="tracker-enroll-optional">optional</span>
                      <input
                        value={newVehicleLabel}
                        onChange={(event) => setNewVehicleLabel(event.target.value)}
                        placeholder="Ambulance 3"
                        maxLength={80}
                      />
                    </label>
                  </div>
                  <div className="tracker-enroll-note">
                    This creates the ambulance and immediately generates its one-time phone registration code.
                  </div>
                  {error && <div className="tracker-enroll-error" role="alert">{error}</div>}
                  <div className="tracker-enroll-actions">
                    <button type="button" disabled={busy} onClick={() => {
                      setAddingVehicle(false);
                      setError('');
                    }}>Cancel</button>
                    <button className="tracker-enroll-primary" type="button" disabled={busy || newVehicleCode.trim().length < 2} onClick={() => void createVehicleAndCode()}>
                      {busy ? 'Creating…' : 'Create ambulance & code'}
                    </button>
                  </div>
                </>
              ) : (
                <>
                  <label className="tracker-enroll-field">
                    Ambulance
                    <select value={vehicleId} onChange={(event) => setVehicleId(event.target.value)}>
                      {vehicles.map((vehicle) => (
                        <option key={vehicle.vehicle_id} value={vehicle.vehicle_id}>
                          {vehicle.vehicle_code}{vehicle.label && vehicle.label !== vehicle.vehicle_code ? ` · ${vehicle.label}` : ''}
                        </option>
                      ))}
                    </select>
                  </label>
                  <button className="tracker-enroll-add" type="button" onClick={() => {
                    setAddingVehicle(true);
                    setError('');
                  }}>
                    + Add ambulance
                  </button>
                  <div className="tracker-enroll-note">
                    Registration codes expire in 10 minutes. Using one replaces the currently registered tracker for that ambulance.
                  </div>
                  {error && <div className="tracker-enroll-error" role="alert">{error}</div>}
                  <button className="tracker-enroll-primary" type="button" disabled={!vehicleId || busy} onClick={() => void createCode()}>
                    {busy ? 'Creating…' : 'Create registration code'}
                  </button>
                </>
              )
            ) : (
              <div className="tracker-enroll-result">
                <span>Registering</span>
                <strong>{selectedVehicle?.vehicle_code ?? 'Ambulance'}</strong>
                <div className="tracker-enroll-code" aria-label={`Registration code ${enrollment.code}`}>{enrollment.code}</div>
                <p>Enter this code on the ambulance phone. It can be used once and expires at {new Date(enrollment.expires_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}.</p>
                <div className="tracker-enroll-result-actions">
                  <button type="button" onClick={() => void navigator.clipboard?.writeText(enrollment.code)}>Copy code</button>
                  <button className="tracker-enroll-primary" type="button" onClick={close}>Done</button>
                </div>
              </div>
            )}
          </section>
        </div>
      )}
    </>
  );
}
