# ADR-001: Recover ephemeral locks from dead holders by inode identity

- Status: Decided
- Date: 2026-10-31

## Context

Ephemeral mode removes the lock file on unlock. The file is created with
`O_CREAT|O_EXCL` and locked with an exclusive flock. The previous holder
unlocked and closed the file before it removed it. The file therefore stayed
on disk for a short window with no flock held on it.

A holder that dies leaves the file on disk with no flock held. The next
contender saw the existing file, acquired the flock on it, dropped the flock,
and retried the create. The file never went away, so the loop never ended. The
lock could not be taken again. Only a manual file removal freed it.

The same window also allowed two holders at once. A contender could acquire the
flock on the existing file while the previous holder was about to remove it.
The removal then unlinked the path under the contender, and a third party
created a new file and locked it. Two processes held the lock on two different
inodes.

The flock is already the liveness oracle. The kernel releases it when the
holder dies. The problem was that the code did not act on that signal.

## Evaluation Criteria

- Recover from a dead holder without a timeout, a PID stamp, or an mtime
  heuristic.
- Keep mutual exclusion. At most one holder at any time.
- Work when several processes share a volume.
- Keep the release path cheap. No polling on the common path.
- Keep `TryLock` non-blocking and `LockContext` cancellable.

## Options

**mtime staleness.** The file is created fresh on every handoff, so its mtime
is the current holder's start time. A contender that takes the flock checks
the mtime age and evicts the file past a threshold.

- Portable. No new platform code. Container-safe.
- Recovery is delayed until the threshold ages out. A creator descheduled
  between the create and the flock for longer than the threshold is wrongly
  evicted. Needs a backoff while waiting for the threshold.

**PID stamp.** The creator writes its PID into the file. A contender reads it
and removes the file when the PID is dead.

- Immediate recovery.
- PID reuse. PID namespaces. On a shared volume, a creator in one container
  and a contender in another can see the same PID number. This breaks mutual
  exclusion across containers, which is a real deployment shape.

**Adopt on flock success without an identity check.** The contender that takes
the flock on the existing file becomes the holder.

- Simple. Recovers the orphan.
- Produces two holders. The previous holder removes the path after the
  adoption, which frees the name for a new file while the adopter still holds
  the old inode. A stress test with 32 goroutines observed two concurrent
  holders and 200 missing-file errors.

**Adopt on flock success with an inode identity check.** The contender that
takes the flock on inode X verifies that the path still points at X. If it
does, the previous holder died before it could remove the path, so the
contender adopts the file and removes it on unlock. If the path is gone or
points at another inode, the contender releases the flock and retries.

- Immediate recovery. No thresholds. No PID semantics. Container-safe.
- Needs a per-platform file identity comparison. Needs the release order
  changed so that the identity check is a valid liveness signal.

## Decision

Adopt on flock success with an inode identity check.

The holder removes the lock file before it releases the flock and closes the
descriptor. The release order is `Remove`, then `platformUnlock`, then
`Close`.

That order makes the identity check a valid liveness oracle. If the path still
points at the inode we just locked, the previous owner never removed it. An
owner that is alive removes the path before it releases the flock. The only
way to reach the flock without removing the path is to die.

Every acquirer passes through the same check. The creator checks too. A
creator that was overtaken while it waited for the flock must release its stale
inode and retry. Without that check the creator enters the critical section
while another process holds a fresh file at the same path.

The check compares the file identity at the path with the identity of the
open descriptor. Unix compares the device and inode numbers. Windows compares
the volume serial number and the file index.

The whole scheme rests on one platform fact. Removing a file while holding a
flock keeps the flock on the now anonymous inode, and a new file at the same
path is an independent lock domain. Unix allows the removal. Windows needs
the handle opened with `FILE_SHARE_DELETE`, which the standard library does
not set.

Two lock paths exist. The blocking path keeps the kernel-blocking flock, so
a handoff costs no polling. The context path uses a non-blocking flock and
waits on the context. The context path is the only way to cancel a wait, and
the blocking path is the only way to avoid a poll interval.

## Implications

**Positive.**

- A dead holder no longer blocks the lock. Recovery is immediate.
- At most one holder at any time. The identity check closes the window that
  produced two concurrent holders.
- The lock file is removed on Windows. The standard library omits
  `FILE_SHARE_DELETE`, so `DeleteFile` failed and the file leaked on every
  unlock.

**Negative.**

- The gate adds a `stat` call per acquisition in ephemeral mode.
- The release order needs `FILE_SHARE_DELETE` on the Windows open path. Both
  open paths need it, because `DeleteFile` requires every open handle to
  allow delete sharing.
- On Windows the file stays delete-pending until the last handle closes. A
  create at that path fails while it is delete-pending. That error is
  transient and the contender retries.
- A waiter on Windows holds a handle to the file while it waits. The
  delete-pending state therefore lasts until every waiter closes its
  handle. A create during that window fails. The retries are bounded and
  they sleep, so the failure becomes a bounded error instead of a hot
  loop.
- A failed removal is swallowed. The lock file survives. The next holder
  adopts it and removes it later.
- The identity check needs per-platform code. Unsupported platforms return
  `fs.ErrUnsupported` before the check is reached.
- The unlock closure reports the error of its first call on every later
  call. The previous behavior returned nil on later calls.

**Unverified.**

- Whether `DeleteFile` succeeds on a `FILE_SHARE_DELETE` handle that holds a
  `LockFileEx` byte range is not confirmed. The removal failure is swallowed,
  so a negative result degrades to the previous behaviour instead of
  breaking.
- The CI matrix covers Linux only. The Windows and AIX or Solaris paths carry
  no automated coverage.

**Out of scope.**

POSIX record locks on AIX and non-illumos Solaris belong to the process, not
to the open file description. Two goroutines in one process do not block each
other. The existing stress test already fails on those platforms. The problem
predates this decision.

## Related

- `pkg/flock/flock.go` — `acquireEphemeral`, `ephemeralUnlock`
- `pkg/flock/flock_windows.go` — open path and `FILE_SHARE_DELETE`
