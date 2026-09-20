# Sync protocol v1 implementation notes

## Trust boundaries

The application encrypts snapshots before the self-hosted service sees them. A 256-bit random master key is wrapped by a separate password-derived key (Argon2id, 64 MiB, 3 iterations, 4 lanes). XChaCha20-Poly1305 uses a fresh random 192-bit nonce per envelope. A domain-separated HMAC authenticates the entire metadata record, including the secrets opt-in. Snapshot associated data binds vault, device and sequence; hashes protect transport lookup and AEAD authenticates content. Keys, pending enrollments/uploads and merge baseline are saved in a separate password-encrypted local envelope, not under the colocated portable config key. Existing DengShell configuration/private-key storage remains governed by the preexisting storage implementation.


The embedded/standalone service serves only an opaque sync protocol over TLS 1.3. Its private root is scoped to the client connection and never installed in OS trust. Short-lived IP SAN leaves rotate under a five-year local root. Per-device 256-bit capabilities are stored as SHA-256 digests in SQLite. Invitations expire, are consumed transactionally, and carry password-wrapped metadata so incorrect passwords do not consume them. Enrollments are journaled locally before server registration; matching retries are idempotent. RestoreOwner is an in-process administrative method only, verifies the master key against local metadata and rotates owner access. There is no remote password reset, unverified TLS fallback or general filesystem/SSH API.

## Consistency and recovery

Each device appends immutable full snapshots. Vector clocks on records track causal updates, including deletion tombstones. Different concurrent values stop with explicit choices. Heads remember highest observed versions and hashes; missing/rolled-back heads and same-sequence forks stop. This detects rollback already seen by a device, not rollback against a brand-new device with no prior state. A malicious storage provider can still deny service or withhold unseen data.

Before publishing, the candidate configuration graph is validated. A durable encrypted pending journal precedes upload. The application checks its current configuration projection again before applying, refusing to overwrite edits made during I/O. Private keys are validated, written with create-exclusive semantics and never silently replaced under an existing ID. Configuration is atomically persisted with pre-sync encrypted backups. Existing display order is retained per device. A history restore is an explicit local edit that goes through the normal sync pipeline on the next cycle.

Backend limits: 16 MiB per snapshot, 128 devices/vector dimensions at the format level, 50,000 entries. The self-hosted backend admits 32 active credentials, 8 pending invitations, 8 concurrent requests, limits failed authorization attempts, and caps encrypted snapshots at 256 MiB. Per-device retention is at most 20 snapshots. Global capacity pressure may remove older history from any device, never its latest head; insertion and pruning commit or roll back together.

## Operational limits

- One active self-hosted sync space per installation.
- Self-hosted integrated mode runs while DengShell runs; no OS service is silently installed.
- Revocation blocks further API access, not recovery of data a former member already decrypted. Rotating the entire vault and affected SSH credentials is needed after compromise. In-place cryptographic rekey is not implemented.
- A root identity/IP change requires deliberate reconfiguration/pairing. No automatic trust downgrade is provided.
- History is bounded and not an offline backup; retention can remove old ciphertext snapshots.

## Validation entry points

`go test ./...`; targeted `go test -race` for syncvault, syncserver and app sync/security-lock tests; `npm run check`; `scripts/test-sync-browser.mjs` against two isolated `--browser --dev --config` backends. Browser fixtures must be generated in private temporary/cache directories; never point these tests at a real user's data directory.

Removed providers are rejected on unlock without changing the saved encrypted profile or local connection data. Disconnect archives that profile, allowing a new self-hosted space to be configured. Historical OAuth configuration files are ignored and not automatically deleted.
