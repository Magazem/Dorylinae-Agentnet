# Crypto & Session Security Summary

## internal/identity

**Purpose:** Manages Ed25519 agent identity. Generates keypair on first run, keeps private key in keystore, caches signed Agent Card.

**Exported Types/Functions:**
- `LoadOrCreate(dir, ks, opts, now)` - Load or create identity with audit reporting
- `NewKeystore(dir, mode)` - Build keystore for config dir ("auto" or "file")
- `NewKeystoreFromEnv(dir)` - Load keystore mode from DORYLINAE_KEYSTORE env
- `Identity` - Loaded identity with card and key backend name
- `Options` - Card creation fields (Name, Harness, Skills)
- `Report`, `CreateDetail` - Audit detail struct

**Crypto Primitives:**
- **Algorithm:** Ed25519 (crypto/ed25519)
- **Key format:** 32-byte Ed25519 seed (base64url encoded on disk)
- **Seed size:** ed25519.SeedSize (32 bytes)
- **Key generation:** `ed25519.GenerateKey(rand.Reader)`
- **Public key encoding:** Base64url via `envelope.KeyString()`

**Key Storage & Protection:**
- Private key exists in memory **only during card creation** (`finish()` function), never handed to callers
- Seed stored in keystore backend (keychain or file fallback)
- Keystore mode selected via DORYLINAE_KEYSTORE env ("auto"=keychain+file fallback, "file"=file only)
- Keychain account derived per config dir: SHA256 of dir path, prefix "identity-"
- **No TODO/FIXME found**

**Card Storage:**
- Signed card cached at `{dir}/agent-card.json` (owner-only readable)
- Card fields: version, name, public_key, harness, skills, created
- Created timestamp truncated to whole seconds UTC (RFC3339 format)
- Signed via `agentcard.Sign(priv, card)`
- Verified via `agentcard.Verify()` on load - fails if key mismatch detected

**Tests (identity_test.go):**
- ✓ First run creates identity, second run reuses
- ✓ Card is immutable after creation
- ✓ Seed not stored in card file
- ✓ Keychain used when available, falls back to file
- ✓ Keychain unavailability logged but doesn't block identity creation (skipped=[])
- ✓ Seed roundtrip verification (Save then Load)

---

## internal/keystore

**Purpose:** Stores one small secret (identity seed) in OS keychain, falling back to owner-only file.

**Exported Types/Functions:**
- `Store` - Tries backends in order (most preferred first)
- `Backend` - Interface: Name(), Get(), Set()
- `NewKeychain(account)`, `NewFile(path)` - Backend constructors
- `AccountFor(dir)` - Derive keychain account from config dir (SHA256[:8] hex)
- `WriteOwnerOnly(path, data)` - Atomic write with owner-only perms
- `ErrNotFound`, `ErrUnavailable` - Error sentinels

**Key Storage Backends:**

**Keychain (keychain.go):**
- Uses `github.com/zalando/go-keyring` for cross-platform keychain
- Service name: "dorylinae"
- Account format: "identity-{SHA256[:8] of config dir}"
- Timeout: 5 seconds per keychain call (prevents hang on locked/absent Secret Service)
- Encoding: Base64url
- Fallback: On timeout or absence, next backend tried

**File (file.go):**
- Path: `{dir}/identity.key`
- Permissions: Owner-only (Unix 0600, Windows DACL)
- Format: Base64url seed + newline
- Atomic write: Create temp with 0600, restrict perms, sync, rename
- Unix: `os.Chmod(path, 0o600)` + verify mode.Perm()&0o077==0 on read
- Windows: SetNamedSecurityInfo with protected DACL (one allow ACE for current user)
- **No TODO/FIXME found**
- **Ignored errors:** `tmp.Close()`, `os.Remove()` in defer (cleanup best-effort)

**Tests (keystore_test.go):**
- ✓ Keychain and file backends independent per config dir (SHA256 separation)
- ✓ Save-verified: readback matches written value
- ✓ ErrNotFound vs ErrUnavailable distinction respected

---

## internal/noise

**Purpose:** Wraps flynn/noise for Noise XX handshake, binds Noise static key to Ed25519 identity, provides transport encryption with explicit counters and replay rejection.

**Exported Types/Functions:**
- `Static` - Noise static key + Ed25519 identity binding
- `NewStatic(identity, sign)` - Generate and bind static key
- `Handshake` - One side of Noise XX with known peer identity
- `NewHandshake(st, peer, initiator)` - Start handshake
- `Transport` - Encrypts/decrypts with explicit counters
- `BindingMessage(staticPub)` - Byte string to sign for binding
- `Prologue(initiator, responder)` - Hash binding input with both identities

**Crypto Primitives:**
- **Pattern:** Noise XX (both sides authenticate)
- **DH:** Curve25519 (DH25519)
- **Cipher:** ChaCha20-Poly1305 (CipherChaChaPoly)
- **Hash:** SHA256
- **Static key binding:** Ed25519 signature over domain-prefixed Noise static public key
- **Binding domain:** "dorylinae-noise-static-v1\n"
- **Prologue domain:** "dorylinae-noise-xx-v1\n"
- **Binding format:** JSON with version, identity (wire form), signature (base64url)

**Handshake Flow:**
- Initiator: `Write() -> (msg1, nil)`, `Read(msg2) -> nil`, `Write() -> (msg3, Transport)`
- Responder: `Read(msg1) -> nil`, `Write() -> (msg2, nil)`, `Read(msg3) -> Transport`
- Binding verified in messages 2 and 3 (payload decoded from JSON)

**Transport Layer:**
- Uses two CipherState instances (CS1→CS2 for directions)
- Counter: 8-byte big-endian, explicit prefix to ciphertext
- AD (additional data): Counter passed to caller's AD function
- Replay detection: `counter < recvNext` fails with `ErrReplay`
- Exhaustion: `sendN == MaxUint64` fails
- **No concurrent use** (type not Mutex-protected)

**Errors:**
- `ErrBinding` - Static key signature doesn't verify
- `ErrHandshake` - Message out of turn, bad format, or crypto failure
- `ErrReplay` - Counter already used or out of order
- `ErrDecrypt` - Authentication failure on Open

**Tests (noise_test.go):**
- ✓ Handshake XX flow and binding verification
- ✓ Transport seal/open and counter ordering
- ✓ Replay rejection
- ✓ Binding tampering caught
- **Ignored errors:** test teardown (`_, _ = tb.Seal(...)` ignores known-good seal)

---

## internal/session

**Purpose:** End-to-end encrypted sessions between paired daemons over relay envelopes. Noise XX handshakes, session.data framing with replay rejection, ping round trip.

**Exported Types/Functions:**
- `Manager` - Runs all sessions for one daemon
- `NewManager(cfg)` - Start manager (returns running instance)
- `Config` - Static, Audit, IsPaired callback, Sender, Wait, PingTimeout
- `Ping(ctx, peer)` - Send encrypted ping, wait up to Config.Wait
- `PingStatus`, `PeerRef`, `Failure` - Ping structs
- `HandleEnvelope(e)` - Queue relay envelope (non-blocking)
- `HandleError(ef)` - Fail pings on relay error

**Session Wire Format:**
- **Types:** session.init (msg 1), session.resp (msg 2), session.fin (msg 3), session.data (encrypted)
- **Session ID:** 16 bytes random (SIDSize)
- **Payload format:** `[16-byte SID][8-byte counter][encrypted data]`
- **Counter:** Explicit, monotonic per session, 8-byte big-endian
- **AD:** Computed per-message: `{dataDomain}\n{from}\n{to}\n{sid}\n{counter}`
- **Datadomain:** "dorylinae-session-data-v1\n"

**Handshake State Machine:**
- Initiator creates session, sends msg1 (SID + Noise msg1)
- Responder receives msg1, creates session, sends msg2 (SID + Noise msg2)
- Initiator receives msg2, sends msg3 (SID + Noise msg3 + Transport), opens session
- Responder receives msg3, opens session, flushes queued messages
- **Any error transitions to failed state** (handshake immutable after fail)

**Message Queuing:**
- Messages to peers without open session queued (max 16 per peer)
- If session exists, message sent immediately
- On session open, queued messages flushed in order
- **Queue dropped on dialing change** (dropout on peer restart)

**Ping Flow:**
1. Caller creates ping ID, marshals `{type:"ping", id: pingID}` as plaintext
2. Manager encrypts on current session or queues + starts handshake
3. Peer receives, unmarshals, replies `{type:"pong", id: pingID}`
4. Manager decrypts, matches by ID, records RTT, closes ping future
5. If no reply within PingTimeout (default 10s), ping fails with timeout

**Limits & Rate Limiting:**
- `maxQueuePerPeer=16` - Reject if queue exceeds
- `maxOpenPerPeer=4` - Older open sessions dropped (keep newest)
- `maxPendingPerPeer=4` - Responder handshakes dropped by age if exceed
- `maxPendingPings=64` - Reject if too many pings in-flight
- `maxPings=1024` - Completed pings forgotten after 1 hour or size exceeded
- `rejectsPerMinute=30` - Audit rate limited
- **Ignored errors:** `m.send(ctx, env)` on flush, ping reply send (logged warn, dropped)

**Audit Actions:**
- `session.open` - New session established (peer, role, session ID prefix)
- `session.reject` - Inbound envelope rejected (peer, type, reason, SID prefix)

**Tests (session_test.go):**
- ✓ Initiator/responder handshake and message exchange
- ✓ Replay rejection
- ✓ Binding tampering detection
- ✓ Ping to peer, RTT measurement
- ✓ Binding failure routing to ping failure
- ✓ Session reuse (second ping no handshake)
- **No TODO/FIXME found**

---

## internal/envelope

**Purpose:** Relay wire format - routed Envelope, control frames, auth signature rules.

**Exported Types/Functions:**
- `Envelope` - Message from daemon to daemon (from, to, team, type, id, ts, payload)
- `Header` - Routing fields only
- `Control` - Control frame (op, nonce, signature, code, card, etc.)
- `ErrorFrame`, `ErrorFrame.Error()`
- `Validate()`, `Parse()`, `ParseHeader()`, `Marshal()`
- `KeyString(pub)`, `ParseKey(s)` - Base64url Ed25519 key codec
- `AuthMessage(nonce)`, `SignAuth(pub, nonce, sign)`, `VerifyAuth(c, nonce)` - Auth signing
- `NewPairCode()`, `FormatPairCode()`, `NormalizePairCode()` - Pairing code helpers
- `CheckCard(raw)` - Validate card size/format (relay-side)

**Key Format:**
- 32-byte Ed25519 public keys, base64url encoded (raw URL-safe padding-less)

**Auth Signature:**
- **Domain:** "dorylinae-relay-auth-v1\n"
- **Message:** Domain + 32-byte nonce (both from relay)
- **Signature:** Ed25519 over domain+nonce
- Verified by relay before envelope forwarding

**Envelope Validation:**
- From/To: Valid base64url Ed25519 keys
- Team: Max 128 chars from [A-Za-z0-9._:-]
- Type: 1-64 chars from [a-z0-9._-]
- ID: 1-128 chars from [A-Za-z0-9._:-]
- TS: RFC3339Nano timestamp
- Payload: Max 1MB total frame

**Pairing Code:**
- Length: 10 characters
- Alphabet: Crockford base32 "0123456789ABCDEFGHJKMNPQRSTVWXYZ" (50 bits entropy)
- Format display: XXXXX-XXXXX (5-5 split)
- User input normalization: Case-insensitive, ignore '-' and spaces, Crockford aliases (O→0, I/L→1)

**Control Frames:**
- Operations: challenge, auth, ready, error, queued, ack, pair_new, pair_code, pair_redeem, pair_peer
- Protocol version: 1
- **No TODO/FIXME found**

---

## internal/agentcard

**Purpose:** Signed Agent Card schema, deterministic JSON for signing, signature verification.

**Exported Types/Functions:**
- `Card` - Payload (version, name, public_key, harness, skills, created)
- `Signed` - Card + signature
- `Skill` - Skill struct (id, name, description)
- `New(pub, name, harness, skills, created)` - Build and validate card
- `Sign(priv, card)` - Sign card with private key
- `Verify(data)` - Verify signed card from JSON
- `Canonical(card)` - Deterministic JSON form

**Crypto:**
- **Algorithm:** Ed25519
- **Signature domain:** "dorylinae-agent-card-v1\n"
- **Canonicalization:** RFC 8785 subset (strict JSON, sorted keys by UTF-16 order)

**Card Schema:**
- Version: 1 (only version supported)
- Name: 1-128 UTF-8 characters, no control chars
- PublicKey: 32-byte base64url Ed25519 public key
- Harness: 1-128 chars, no control chars
- Skills: Array of {id, name, description} (description optional)
- Created: RFC3339 UTC, whole seconds (e.g., "2026-03-04T05:06:07Z")

**Signing Process:**
1. Card validated (version, text lengths, key format, created format)
2. Canonical JSON form computed (RFC 8785 rules)
3. Domain prefix prepended
4. Ed25519 signature generated over domain+canonical
5. Signature base64url encoded

**Verification Process:**
1. Envelope parsed as generic JSON (strict: no invalid UTF-8, no duplicate keys, no trailing data)
2. Signature verified **first** (before schema validation)
3. If signature OK, card re-parsed strictly (disallows unknown fields)
4. Schema validation applied

**Canonicalization:**
- Strict JSON parsing (errors on invalid UTF-8, duplicate keys)
- Object keys sorted by UTF-16 code unit order
- Escape sequences: standard JSON escapes + \uXXXX for control chars
- Numbers: Integers only, no floats, no leading zeros, no -0

**Tests (agentcard_test.go, canonical.go):**
- ✓ New() validates all card fields
- ✓ Sign() checks key match, canonical form correct
- ✓ Verify() accepts valid, rejects any tampering (field change, added field, signature change)
- ✓ Canonical form matches deterministic rules
- **No TODO/FIXME found**

---

## internal/peers

**Purpose:** Stores paired agents, runs daemon-side of pairing. Verifies received Agent Cards before storage.

**Exported Types/Functions:**
- `Manager` - Runs pairings for one daemon
- `NewManager(cfg)` - Create manager (not running, no goroutines)
- `Config` - Store, Audit, Card, Sender, Wait, Now, Logger
- `Start(ctx)` - Ask relay for pairing code
- `Redeem(ctx, code)` - Redeem user-entered code
- `Get(id)` - Get status of pairing
- `List(ctx)` - Get paired peers
- `Status`, `Peer`, `Failure` - Status structs
- `Store` - Reads/writes peers table
- `Store.Add(ctx, card, raw, at)` - Store peer from verified card
- `Store.List(ctx)` - List all peers ordered by pairing time

**Pairing Flow:**
- **Issuer (pair --new):**
  1. Sends pair_new to relay with local card
  2. Relay issues code, sends pair_code frame (code + expiry)
  3. Redeemer receives and enters code
- **Redeemer (pair <code>):**
  1. Parses code (normalized), sends pair_redeem to relay with code + local card
  2. Relay validates code, matches with issuer
  3. Relay sends issuer the redeemer's card (pair_peer frame)
  4. Relay sends redeemer the issuer's card (pair_peer frame)
- **Store:**
  1. Each side receives pair_peer with peer's card
  2. Card signature verified (agentcard.Verify)
  3. If valid, stored in peers table (refresh if already paired)

**Database Schema (peers table):**
- public_key (PK) - base64url Ed25519 public key
- name, harness - from card
- skills - JSON array
- card - raw envelope as received
- paired_at - RFC3339 timestamp (original pairing time, kept on refresh)

**Pairing ID:**
- Format: "pair-" + 16 hex digits (8 random bytes)
- Used to correlate relay frames to local pairings

**Pairing Code:**
- Generated via `envelope.NewPairCode()` (relay-side)
- Format: 10 Crockford base32 chars, displayed as XXXXX-XXXXX
- User entry normalized via `envelope.NormalizePairCode()`
- Expiry: Configurable, default 10 minutes + 2s grace

**Timeout/Limits:**
- `noReplyTimeout=30s` - No relay reply → timeout failure
- `expiryGrace=2s` - Grace period after expiry for in-flight messages
- `keepFinished=1h` - Completed/failed pairings forgotten after 1 hour
- `maxPending=16` - Reject if too many in-progress pairings

**Audit Actions:**
- `pair.start` - New pairing started (id, role)
- `pair.complete` - Pairing succeeded (id, role, peer public key)
- `pair.fail` - Pairing failed (id, role, code, reason)

**Tests (peers_test.go):**
- ✓ Issuer/redeemer flow with relay
- ✓ Code expiry timeout
- ✓ Bad card rejection
- ✓ Peer list and re-pairing refresh

**No TODO/FIXME found**

---

## internal/capability

**Placeholder:** No implementation. Will issue and verify daemon-issued capability tokens. Currently empty except doc comment.

---

## internal/protocol

**Placeholder:** No implementation. Will define wire messages and schemas (live in Docs/protocol/). Currently empty except doc comment.

---

## internal/transport

**Placeholder:** No implementation. Will implement Noise-secured sessions and relay connections. Currently empty except doc comment.

---

## tools/verifycard

**Purpose:** Standalone tool (no shared code except stdlib) to verify signed Agent Card from stdin.

**Input:**
- Signed-card envelope: `{"card": {...}, "signature": "..."}`
- Or agentnet identity --json output (extra fields ignored)
- stdin limit: 1 MB

**Parsing:**
- Reject if input not valid UTF-8
- Reject duplicate object keys
- Reject trailing data after JSON value
- Handle Windows PowerShell BOM (0xEF 0xBB 0xBF prefix)

**Verification:**
1. Parse envelope strictly (as above)
2. Extract card and signature (base64url decode)
3. Compute canonical JSON form of card (RFC 8785 subset: sorted keys by UTF-16 order, integers only)
4. Verify Ed25519 signature over domain+canonical
5. If valid, re-parse card with schema validation

**Canonical Form (RFC 8785 subset):**
- Null → "null"
- Bool → "true" or "false"
- String → JSON-escaped, quoted
- Number → Integers only, reject floats and non-canonical forms (leading zeros, -0)
- Array → Sorted recursively
- Object → Keys sorted by UTF-16 code unit order, values canonicalized recursively

**Output:**
- Stdout on success: "OK "<name>" <pubkey_base64url>"
- Stderr on invalid: "verifycard: INVALID: <reason>"
- Stderr on malformed: "verifycard: <reason>"

**Exit Codes:**
- 0: Signature valid
- 1: Signature invalid (errInvalid)
- 2: Malformed or unreadable input (errMalformed)

**No TODO/FIXME found**

---

## Cross-Cutting Security Properties

**Private Key Handling:**
- Ed25519 private keys generated via crypto/rand (cryptographically secure)
- Never logged or cached outside keystore
- Never appear in audit logs or envelopes
- Keychain timeout (5s) prevents hang on locked Secret Service

**Signature Verification:**
- All signatures verified before use (relay auth, pairing cards, session binding)
- Signature domain strings prevent cross-protocol attacks
- Canonical JSON + strict parsing prevents tampering

**Replay Protection:**
- Session transport uses explicit monotonic counters
- Handshake nonces bound to both identities (prologue)
- Pairing codes single-use (relay state)

**Key Isolation:**
- Keystore backends independent by config dir (SHA256 separation)
- File permissions (Unix 0600, Windows DACL) prevent unprivileged access
- Temporary files restricted before secret bytes written

**Untested Areas:**
- Keychain timeout path (requires slow/hanging Secret Service mock)
- Windows DACL permissions (perm_windows.go not covered by CI on Unix)
- Pairing code rate limiting (relay-side only, not in these packages)
- Session manager goroutine shutdown under load

**Known Security Shortcuts:**
- Relay error handling: Ignored send() errors on flush/reply (daemon logs warn, drops message)
- Session.data counter overflow: Rejects at MaxUint64 (practical limit, not prevented ahead)

