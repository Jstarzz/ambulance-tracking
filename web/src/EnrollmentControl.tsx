import { useEffect, useMemo, useState } from 'react';

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

export default function EnrollmentControl() {
  const [vehicles, setVehicles] = useState<Vehicle[]>([]);
  const [available, setAvailable] = useState(false);
  const [open, setOpen] = useState(false);
  const [vehicleId, setVehicleId] = useState('');
  const [enrollment, setEnrollment] = useState<EnrollmentResponse | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

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

  async function createCode() {
    if (!vehicleId || busy) return;
    setBusy(true);
    setError('');
    setEnrollment(null);
    try {
      const csrf = sessionStorage.getItem('csrf_token') ?? '';
      const response = await fetch(`/api/v1/vehicles/${encodeURIComponent(vehicleId)}/enrollment`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'X-CSRF-Token': csrf }
      });
      const body = await response.json().catch(() => ({})) as Partial<EnrollmentResponse> & { error?: string };
      if (!response.ok || !body.code || !body.expires_at) {
        throw new Error(body.error || 'Could not create registration code.');
      }
      setEnrollment(body as EnrollmentResponse);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not create registration code.');
    } finally {
      setBusy(false);
    }
  }

  function close() {
    setOpen(false);
    setEnrollment(null);
    setError('');
  }

  return (
    <>
      <button className="tracker-enroll-launch" type="button" onClick={() => setOpen(true)}>
        Register tracker
      </button>

      {open && (
        <div className="tracker-enroll-backdrop" role="presentation" onMouseDown={(event) => {
          if (event.target === event.currentTarget) close();
        }}>
          <section className="tracker-enroll-dialog" role="dialog" aria-modal="true" aria-labelledby="tracker-enroll-title">
            <div className="tracker-enroll-head">
              <div>
                <h2 id="tracker-enroll-title">Register tracker</h2>
                <p>Create a one-time code for an ambulance phone.</p>
              </div>
              <button className="tracker-enroll-close" type="button" onClick={close} aria-label="Close">×</button>
            </div>

            {!enrollment ? (
              <>
                <label className="tracker-enroll-field">
                  Ambulance
                  <select value={vehicleId} onChange={(event) => setVehicleId(event.target.value)}>
                    {vehicles.map((vehicle) => (
                      <option key={vehicle.vehicle_id} value={vehicle.vehicle_id}>
                        {vehicle.vehicle_code}{vehicle.label ? ` · ${vehicle.label}` : ''}
                      </option>
                    ))}
                  </select>
                </label>
                <div className="tracker-enroll-note">
                  The code expires in 10 minutes and replaces the currently registered tracker for this ambulance when used.
                </div>
                {error && <div className="tracker-enroll-error" role="alert">{error}</div>}
                <button className="tracker-enroll-primary" type="button" disabled={!vehicleId || busy} onClick={() => void createCode()}>
                  {busy ? 'Creating…' : 'Create registration code'}
                </button>
              </>
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
