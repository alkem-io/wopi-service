# WOPI Locking, Collabora Sessions & Dead-Session Recovery

Context notes for anyone touching document locking, in-editor rename, the
replace-file guard, or debugging a "read-only" / "cannot be saved" Collabora
document. Everything here is **grounded in the code as of this writing** — each
claim cites the file it comes from. Where a widely-repeated piece of WOPI folklore
does **not** match our implementation, it is called out explicitly. Verify against
the cited source before relying on a number; do not trust manual-testing memory.

---

## 1. The model in one paragraph

Collabora Online (`coolwsd`) is the WOPI **client**; this service is the WOPI
**host** and owns all lock state. A lock is **per file, not per user**: Collabora's
DocBroker takes **one** opaque lock ID at the start of the first edit session and
holds it for the entire collaborative session (a 5-person co-edit is still one
lock), refreshing it periodically, then issues a single Unlock at teardown.
Real-time co-authoring is handled inside `coolwsd` — the host never sees who is
editing, only that the document is open. This is why "who contributed" is derived
from the `access_tokens` table (windowed distinct actor IDs), **never** from lock
metadata.

The `locks` table enforces this: `UNIQUE (file_id)` — at most one lock row per file.
(`migrations/000002_create_locks.up.sql`)

> **Which service owns locks?** `wopi-service` (this Go service) is the lock/token
> authority. Do **not** confuse it with `collaborative-document-service`, which is the
> Hocuspocus/Yjs realtime server for whiteboards/memos and has nothing to do with WOPI
> locks. (This exact mix-up cost time in an earlier session.)

---

## 2. The numbers (verified — do not trust the folklore values)

| Quantity | Value | Source | Notes |
|---|---|---|---|
| **Lock expiry** (`DefaultLockDuration`) | **30 min** | `internal/domain/model/lock.go:10` (test asserts it: `lock_test.go:28`) | Set as `expires_at = now + 30m` on Lock **and** on every RefreshLock. This is the WOPI-standard orphan-lock window. |
| **Zombie-takeover threshold** (`MaxLockLifetime`) | **4h** default | `internal/config/config.go:140` (`WOPI_MAX_LOCK_LIFETIME`, default `"4h"`; `0` disables) | A *different* number from the expiry. Gates the age-based takeover (see §4). |
| **Access-token TTL** (`defaultTokenTTL`) | **8h** | `internal/domain/service/token_service.go:21` | Comfortably longer than the 30-min lock + refresh cadence. |
| **Cleanup sweep interval** | **15 min** | `internal/domain/service/cleanup_service.go:28` | Deletes expired tokens **and** expired locks. Housekeeping only — not on the self-heal path (see §3). |

> ⚠️ **Common mis-reading:** a lock row whose `created_at → expires_at` span is
> ~45 min does **not** mean the TTL is 45 min. `created_at` is never bumped on
> refresh (only `expires_at` moves — `wopi_service.go` Lock refresh branch), so a
> 45-min span just means the last RefreshLock landed ~15 min after creation
> (`created + 30m` from that refresh). The TTL is **30 min**, always measured from
> the *last* refresh.
>
> ⚠️ **Token TTL is an absolute epoch, not a duration.** The `access_token_ttl`
> handed to Collabora is `expiresAt.UnixMilli()` — an **absolute UNIX timestamp in
> ms** (`token_service.go:111-113`), not "8h from now" as a delta. A client bug once
> fed it straight into `setTimeout(fn, accessTokenTTL)` as if it were a duration
> (scheduling a wakeup ~57 years out); the correct form is
> `msUntilExpiry = accessTokenTTL - Date.now()`. Remember this if anything downstream
> consumes the TTL as a timer.

---

## 3. Lock lifecycle & the self-heal path

**Read filtering is the key mechanism.** `FindLockByFileID` is
`WHERE file_id = $1 AND expires_at > now()`
(`internal/adapter/outbound/postgres/generated/locks.sql.go:46`). Expired locks are
invisible to the read path. Consequences:

- An orphaned lock (session crashed without Unlock) **stops blocking new opens the
  moment it expires** — i.e. ≤30 min after the last refresh — because the next
  `Lock` sees no active lock and acquires fresh. No sweep or takeover required.
- The 15-min cleanup sweep (`cleanup_service.go`) only **garbage-collects** the dead
  rows afterwards; it is **not** what unblocks the document.

Lock state machine (`internal/domain/service/wopi_service.go`, `Lock`):

1. No active lock → acquire, return 200.
2. Existing lock, **same** lockID → refresh expiry (`now + 30m`), return 200.
3. Existing lock, **different** lockID, within `MaxLockLifetime` → **409 Conflict**
   with `X-WOPI-Lock: <current lock id>` (the handler sets that header —
   `wopi_handler.go` `handleLockError`).
4. Existing lock, **different** lockID, **past** `MaxLockLifetime` → atomic takeover
   (see §4).

`PutFile` (`wopi_service.go`): rejects with 409 only if a lock exists and the
provided lockID is empty or mismatched. **If the file is unlocked, the write
proceeds.** This means a trailing PutFile that arrives *after* Unlock/expiry still
saves — see §5.1.

---

## 4. Zombie-lock takeover — what it is and its real limits

`Lock` case 4: when a new lockID requests a file whose existing lock is **older than
`MaxLockLifetime`** (`now - created_at > MaxLockLifetime`, default 4h), the new
session takes the lock over (atomic CAS on `lock_id`, resets `created_at`/
`expires_at`; loses gracefully on a race — refetches and returns the real current
lock). This defends against a DocBroker that refreshes its lock forever, blocking
every new session.

**Two honest limitations, both relevant to real incidents:**

1. **It is gated on lock _age_, not owner _liveness_.** A crashed session's lock is
   not "taken over" early just because its owner is provably dead — the new session
   waits out the age threshold. With the default 4h, the takeover is effectively
   irrelevant to crash recovery; the **30-min expiry** (§3) is what actually frees a
   crashed lock first.
2. **The `locks` table records no owner.** Columns are `id, file_id, lock_id,
   expires_at, created_at` (`migrations/000002_create_locks.up.sql`) — there is
   **no** token/actor/session reference. So a "replace the lock if its owning token
   is dead" check (a liveness-gated takeover) is **not** implementable against the
   current schema without first adding an owner column. Noted as a design
   consideration, not a quick fix — see §7.

---

## 5. Failure modes we've actually hit (and generic ones, marked)

### 5.1 Unlock-before-final-PutFile race — **tolerated** (not a live bug for us)
Classic WOPI hazard: in a load-balanced host, an Unlock can overtake the trailing
PutFile; the PutFile then hits an unlocked doc. **Our `PutFile` allows writes to an
unlocked file** (§3), so a late PutFile still saves — no grace-window hack needed,
*as long as no other lock has since been acquired*. We do not rely on sticky routing
for correctness of this case. (Generic folklore says hosts hard-409 this; we don't.)

### 5.2 Stale lock after a crash → document opens **read-only** — **hit, root-caused**
**Symptom:** the document loads and shows content, but Collabora renders the
**viewer** ribbon (only *File / View / Help*), not the editor ribbon. Bottom bar may
still say "Saved".
**Root cause:** a lock row left by a session that died without Unlock. On reopen the
new lockID differs from the ghost's → `Lock` returns **409** → Collabora degrades to
read-only. Trace signature in the wopi logs, per open:
`GET .../files/{id}` 200 → `GET .../contents` 200 → `POST .../files/{id}` **409**.
**Self-heal:** happens automatically at the lock's 30-min expiry (§3). To unblock
immediately, delete the stale row (§6). A **service restart does _not_ clear it** —
the lock lives in Postgres, not in the restarted process (there is currently **no
boot-time lock sweep**; see §7).

### 5.3 In-editor rename opening a *second* session while the first holds the lock — **hit**
An earlier iteration remounted / reloaded the Collabora iframe on a header rename.
The reload opened a **new** Collabora session while the **old** session still held
the WOPI lock → the new session hit a lock conflict and came up **read-only**
("the UI of Excel is fucked up"). Fix was to **not** remount the iframe on rename
(use Collabora's in-place `Document_Loaded` refresh instead). Lesson: never force a
fresh Collabora session against a document that already has a live lock from the
same user's still-open session.

### 5.4 PutRelativeFile poison after a broker/image mismatch — **hit** (lock-adjacent)
Not a lock bug, but it lands users in the same read-only/"cannot be saved" territory
and is easy to confuse with §5.2. If `CheckFileInfo` advertises
`SupportsRename: false` **and** does not set `UserCanNotWriteRelative: true`,
Collabora's rename falls back to **PutRelativeFile** (`X-WOPI-Override: PUT_RELATIVE`),
which this service does not handle → **400 "unknown X-WOPI-Override"** → DocBroker
marks the doc in conflict → *"Document cannot be saved."* Current code advertises
rename and **does** set `UserCanNotWriteRelative: true` (`wopi_service.go:141-143`),
so a rename-capable, broker-configured build never triggers this. It was seen when a
released image **older than the rename feature** was deployed (see the rename docs).

### 5.5 Refresh-lock retry storm after token expiry — **generic, low risk for us**
Folklore: when the WOPI token goes invalid mid-session, `coolwsd` retries
RefreshLock forever while the host rejects it. Low risk here because the token TTL is
**8h** (§2), far longer than the 30-min lock/refresh cadence — a session would have
to outlive 8h of continuous editing to reach it. Worth remembering if the token TTL
is ever shortened toward the lock duration.

### 5.6 RENAME_FILE without `X-WOPI-Lock` — **tolerated**
Some `coolwsd` versions send RENAME_FILE without the lock header even though the spec
requires it. Our `renameFile` handler (`wopi_handler.go`) requires only a valid token
+ `write` permission + document existence — it does **not** require `X-WOPI-Lock`, so
this never fails for us.

---

## 6. Break-glass: clearing a stuck lock

When a document is stuck read-only after a crash/restart and you don't want to wait
out the ≤30-min expiry, delete the specific lock row. **Pin the row you inspected** so
a lock acquired by a new legitimate session between inspection and delete is not
nuked:

```sql
-- inspect first
SELECT file_id, lock_id, created_at, expires_at, now() > expires_at AS expired
FROM locks WHERE file_id = '<document-id>';

-- delete, pinned to the exact row observed
DELETE FROM locks
WHERE file_id = '<document-id>'
  AND lock_id = '<lock-id-you-saw>'
  AND expires_at = '<expires_at-you-saw>';
```

Locks live in **Postgres** (`wopi` database, table `locks`), not Redis. In the local
dev stack the `wopi` database lives in the **shared** Postgres container
`alkemio_dev_postgres`, and the superuser is whatever `POSTGRES_USER` resolves to in
`server/.env.docker` (in the reference setup, `synapse`):

```bash
cd server   # for .env.docker
docker exec -e PGPASSWORD="$(grep -E '^POSTGRES_PASSWORD=' .env.docker | cut -d= -f2)" \
  alkemio_dev_postgres \
  psql -U "$(grep -E '^POSTGRES_USER=' .env.docker | cut -d= -f2)" -d wopi \
  -c "DELETE FROM locks WHERE file_id='<id>' AND lock_id='<lock>' AND expires_at='<expires_at-you-saw>';"
```

Pin `expires_at` to the value you inspected (as in the SQL block above), so a lock
re-acquired by a new legitimate session between inspection and delete is not removed.

It is safe to delete a lock whose owning `coolwsd` DocBroker is gone (e.g. after a
Collabora restart): nothing live will ever send a RefreshLock/Unlock/PutFile carrying
that lock ID, so the row is a pure tombstone — the only thing it still does is force
409s until it expires.

---

## 7. Proposed improvements (NOT implemented — evaluate before building)

These came up while debugging the read-only-after-crash class. None are in the code
today; each has a real caveat.

1. **Boot-time lock sweep.** On startup, drop locks that cannot belong to a live
   session. The simplest safe version deletes **already-expired** locks at boot
   (a subset of what the 15-min sweep does, just run immediately) — cheap and correct.
   A stronger "delete every lock, since a fresh process owns no DocBrokers" is
   tempting but **wrong in a multi-replica deployment**: another replica may hold live
   locks. Scope carefully to single-instance/dev, or gate on replica awareness.
2. **Liveness-gated takeover.** Take a lock over as soon as its owner is provably
   dead, instead of waiting `MaxLockLifetime`. **Blocked on schema:** the `locks`
   table has no owner reference (§4), so there is nothing to join against
   `access_tokens` to decide liveness. Would require adding an owning-token/actor
   column to `locks` and populating it on Lock. Only worth it if the 30-min expiry
   window proves too long in practice.
3. **Shorten `MaxLockLifetime` toward 30 min.** Blunt backstop. Note it mostly does
   nothing for crash recovery today because the 30-min **expiry** already frees
   crashed locks first; lowering it only matters for a session that keeps
   *successfully refreshing* a lock it shouldn't (a true zombie DocBroker), which is
   rarer.

Rule of thumb before changing lock timing: the WOPI expiry exists for the case where
the host **cannot tell** whether `coolwsd` is alive. It guarantees "don't lock
*forever*," not "always wait the full window." If we ever have positive evidence an
owner is dead (and a way to attribute a lock to that owner), acting on it early is
legitimate — but that attribution does not exist in the schema yet.

---

## 8. Related surfaces

- **Replace-file guard / `lock-status` endpoint.** `GET /wopi/files/{id}/lock-status`
  (`wopi_handler.go` `LockStatus`) reports whether a document currently has an active
  lock, so alkemio-server can refuse to swap a document's backing file while it is
  being edited. It is gated by the server-trusted actor header (not a WOPI token,
  since it is not a Collabora callback) and reads through the same
  expiry-filtered `FindByFileID`, so a stale/expired lock reports **unlocked**.

  The **server side** turns this into a deliberate **3-way** decision
  (`wopi.service.adapter` `getLockStatus` → `'locked' | 'unlocked' | 'unavailable'`;
  server PR **#6240**, commit `754b05e37`), and the asymmetry matters:
  - `200 {locked:true}` → **`locked`** → refuse the swap (someone is editing).
  - `200 {locked:false}` → **`unlocked`** → proceed.
  - **`404`** (route missing → wopi image predates this endpoint, shipped in #25) →
    **`unlocked`** → **proceed**, with an ERROR logged. A definitively-absent feature
    must not permanently block replace. *This was a real incident:* a stale wopi
    container 404'd the route and the old fail-closed guard read that as "locked",
    surfacing a false *"This document is currently being edited."*
  - malformed 200 body / non-404 HTTP error / network-timeout → **`unavailable`** →
    **refuse (fail closed) with a distinct message**. Here we genuinely can't tell, so
    err on the side of not clobbering a possible live edit.

  So it fails **open** only on an unambiguous 404 (feature not deployed) and fails
  **closed** on every "can't tell" case.

  **Residual races (deferred, from the 014 replace-file work — not bugs to fix
  blindly, but know they exist):**
  - *Swap-vs-live-editor:* `lock-status` can report `unlocked`, a user then acquires a
    lock, and the swap deletes the old file under the now-live editor. FR-013 narrows
    the window but does not close it — documented residual risk.
  - *Read-only viewer not notified on replace:* a viewer holds no lock, so a replace
    correctly proceeds; their open session then points at the deleted file with no
    notification. Known gap, deferred.
  - *Stale-token/lock purge on replace:* `DeleteByFileID` (purge tokens/locks for a
    replaced-out file) was explicitly left out of scope.
- **In-editor rename.** See the rename feature docs — the rename event path and the
  `SupportsRename` / `UserCanNotWriteRelative` interplay that governs §5.4.
