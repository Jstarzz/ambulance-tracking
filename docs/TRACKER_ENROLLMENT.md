# Tracker enrollment

Ambulance workers do not configure server URLs or handle long-lived device credentials.

## Flow

1. A dispatcher signs in to the dispatcher web app and chooses **Register tracker**.
2. The dispatcher selects an ambulance and creates an 8-character registration code.
3. The code expires after 10 minutes and can be used once.
4. The Android tracker sends the code and its device model to `POST /api/v1/device/enroll`.
5. The server resolves the ambulance from the code, generates the long-lived device credential, retires the previously-active tracker for that ambulance, and returns the new credential once.
6. Android stores the credential with the existing Keystore-backed `SecureConfig` implementation. The worker never sees it.
7. Normal tracker sessions continue to use the existing short-lived `/api/v1/device/session` exchange.

The production API base URL is deployment configuration. Android builds read `EMS_API_BASE_URL`; if it is not set, the project default is `https://tracking.itsjosiahdavis.dev`.

## Security properties

- Registration codes are stored as SHA-256 hashes, not plaintext.
- Creating a new code invalidates older unused codes for that ambulance.
- Consuming a code is transactional and marks it used.
- Re-enrollment enforces the MVP invariant of one active tracker per ambulance and revokes sessions belonging to the retired tracker.
- The generated long-lived device credential is returned only during successful enrollment and then stored encrypted on Android.
