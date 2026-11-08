// Package flock provides a function-based interface to advisory file
// locking.
//
// The locking functions return unlock, a closure that releases the lock
// and closes the file descriptor. unlock is safe to call more than once.
// A second or later call returns the error of the first call. TryLock and
// TryRLock return ErrLockHeld with a nil unlock when the lock is
// unavailable.
//
// The package supports two modes. The default mode opens or creates the
// lock file and leaves it on disk after unlock, like the traditional flock
// utility. WithEphemeral removes the lock file on unlock.
//
// Ephemeral mode hands the file from one holder to the next. The acquirer
// creates the file with O_CREAT|O_EXCL and takes an exclusive flock. The
// holder removes the file before it releases the flock. Every acquirer
// verifies that the path still points at the locked file. An acquirer that
// finds a file without a flock adopts it and removes it on unlock. A dead
// holder therefore does not block the lock.
//
// Locking behaviour is not guaranteed to be identical across platforms.
// Some UNIX-like operating systems transparently upgrade a shared lock to
// an exclusive lock. On AIX and non-illumos Solaris two shared locks on
// the same inode through different descriptors cannot coexist.
package flock

import (
	"context"
	"errors"
	"io/fs"
	"math/rand"
	"os"
	"sync"
	"time"
)

var (

	// ErrLockHeld is returned by TryLock and TryRLock when the lock cannot be
	// acquired without blocking.
	ErrLockHeld = errors.New("lock held by another process")

	ErrEmptyPath = errors.New("lockfile path is empty")

	ErrRLockNotSupported = errors.New("WithEphemeral is not supported with RLock")
)

// Option configures a lock call.
type Option func(*config)

type config struct {
	perm      fs.FileMode
	ephemeral bool
}

// WithPermissions sets the OS permissions applied when a new lock file is
// created. It has no effect when the file already exists. The default is
// 0o600.
func WithPermissions(perm fs.FileMode) Option {
	return func(c *config) { c.perm = perm }
}

// WithEphemeral makes the lock file transient. Unlock removes the file.
// The acquirer creates the file with O_CREAT|O_EXCL and takes an exclusive
// flock. The holder removes the file before it releases the flock. An
// acquirer adopts a file that no holder keeps locked. Do not combine this
// option with the read-lock family.
func WithEphemeral() Option {
	return func(c *config) { c.ephemeral = true }
}

// Stat returns the FileInfo for the lock file. It is a convenience wrapper
// around os.Stat for callers that want to inspect lock metadata without
// holding a lock.
func Stat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}

// Lock acquires an exclusive lock on path. It blocks until the lock is
// available. unlock releases the lock and closes the file descriptor.
// A second call to unlock returns the error of the first call.
func Lock(path string, opts ...Option) (unlock func() error, err error) {
	return acquire(context.Background(), path, modeExclusive, blocking, opts)
}

// TryLock attempts to acquire an exclusive lock on path without blocking.
// It returns ErrLockHeld with a nil unlock when the lock is unavailable.
func TryLock(path string, opts ...Option) (unlock func() error, err error) {
	return acquire(context.Background(), path, modeExclusive, tryOnce, opts)
}

// RLock acquires a shared (read) lock on path. It blocks until the lock
// is available. Multiple processes can hold shared locks at the same
// time. unlock releases the lock and closes the file descriptor. Calling
// unlock more than once returns the error of the first call.
func RLock(path string, opts ...Option) (unlock func() error, err error) {
	return acquire(context.Background(), path, modeShared, blocking, opts)
}

// TryRLock attempts to acquire a shared (read) lock on path without
// blocking. It returns ErrLockHeld with a nil unlock when the lock is
// unavailable.
func TryRLock(path string, opts ...Option) (unlock func() error, err error) {
	return acquire(context.Background(), path, modeShared, tryOnce, opts)
}

// LockContext acquires an exclusive lock on path. It retries until the
// lock is available or ctx fires. retryDelay controls the wait between
// attempts. A retryDelay of zero or less defaults to 50ms.
func LockContext(
	ctx context.Context,
	path string,
	retryDelay time.Duration,
	opts ...Option,
) (unlock func() error, err error) {
	return acquireContext(ctx, path, modeExclusive, retryDelay, opts)
}

// RLockContext acquires a shared (read) lock on path. It retries until
// the lock is available or ctx fires. retryDelay controls the wait
// between attempts. A retryDelay of zero or less defaults to 50ms.
func RLockContext(
	ctx context.Context,
	path string,
	retryDelay time.Duration,
	opts ...Option,
) (unlock func() error, err error) {
	return acquireContext(ctx, path, modeShared, retryDelay, opts)
}

type lockMode int

const (
	modeExclusive lockMode = iota
	modeShared
)

type lockAttempt int

const (
	// tryOnce forbids waiting. The call returns ErrLockHeld instead.
	tryOnce lockAttempt = iota
	// blocking waits in the kernel until the lock becomes available.
	blocking
	// withContext waits for the caller between attempts.
	withContext
)

// flockFlag is the per-platform F_SETLK argument. The value flockExclusive
// maps to LOCK_EX or LOCKFILE_EXCLUSIVE_LOCK. The value flockShared maps to
// LOCK_SH or 0.
type flockFlag int

const (
	flockExclusive flockFlag = iota
	flockShared
)

// acquire is the shared body of every lock call.
func acquire(
	ctx context.Context,
	path string,
	mode lockMode,
	attempt lockAttempt,
	opts []Option,
) (unlock func() error, err error) {
	if path == "" {
		return nil, ErrEmptyPath
	}
	cfg := config{perm: 0o600}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.ephemeral && mode == modeShared {
		return nil, ErrRLockNotSupported
	}

	if cfg.ephemeral {
		return acquireEphemeral(ctx, path, cfg, attempt)
	}
	return acquireTraditional(path, cfg, mode, attempt != blocking)
}

// acquireContext calls acquire in a loop until the caller holds the lock
// or ctx fires. The function waits retryDelay between attempts.
func acquireContext(
	ctx context.Context,
	path string,
	mode lockMode,
	retryDelay time.Duration,
	opts []Option,
) (unlock func() error, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if retryDelay <= 0 {
		retryDelay = 50 * time.Millisecond
	}
	timer := time.NewTimer(retryDelay)
	defer timer.Stop()
	for {
		unlock, err := acquire(ctx, path, mode, withContext, opts)
		if err == nil {
			return unlock, nil
		}
		if !errors.Is(err, ErrLockHeld) {
			return nil, err
		}
		timer.Reset(retryDelay)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// acquireTraditional opens or creates the lock file, takes the flock in
// exclusive or shared mode, and returns unlock. unlock is idempotent and
// safe to call concurrently.
func acquireTraditional(
	path string,
	cfg config,
	mode lockMode,
	try bool,
) (unlock func() error, err error) {
	fh, err := openLockFile(path, cfg)
	if err != nil {
		return nil, err
	}

	flag := flockExclusive
	if mode == modeShared {
		flag = flockShared
	}
	flockFn := platformFlock
	if try {
		flockFn = platformFlockTry
	}
	if err := flockFn(fh, flag); err != nil {
		_ = fh.Close()
		if errors.Is(err, ErrLockHeld) {
			return nil, ErrLockHeld
		}
		return nil, err
	}
	return idempotentClose(fh), nil
}

// ephemeralRetryDelay is the wait between two ephemeral attempts. The wait
// keeps a lost race from turning into a hot loop. A short delay keeps the
// handoff latency low.
const (
	ephemeralRetryDelay       = 50 * time.Microsecond
	ephemeralRetryDelayJitter = 50 * time.Microsecond
)

// maxVanishedRetries limits the retries after the lock file vanished. The
// window ends when the last handle to the file closes. The count and
// ephemeralRetryDelay set the total tolerance of the retry loop.
const maxVanishedRetries = 1000

// errLockFileVanished marks a lock file that disappeared during the
// acquisition. The caller retries.
var errLockFileVanished = errors.New("lock file vanished")

// acquireEphemeral runs the O_CREAT|O_EXCL retry loop. Every acquirer
// passes the identity gate before it enters the critical section. The gate
// compares the file at the path with the locked descriptor. The acquirer
// holds the lock only if both point at the same file. Otherwise the
// acquirer releases the stale file and retries.
//
// The blocking mode waits in the kernel until the current holder releases
// the flock. The context mode waits for the caller between attempts. The
// try mode never waits and makes at most two attempts.
func acquireEphemeral(
	ctx context.Context,
	path string,
	cfg config,
	attempt lockAttempt,
) (unlock func() error, err error) {
	isBlocking := attempt == blocking
	maxAttempts := 0
	if !isBlocking {
		maxAttempts = 2
	}

	vanished := 0
	for i := 0; maxAttempts == 0 || i < maxAttempts; i++ {
		fh, created, err := openEphemeralCreate(path, cfg)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, errLockFileVanished) {
				// The file vanished between the create and the open. Retry.
				vanished++
				if vanished > maxVanishedRetries {
					return nil, err
				}
				if attempt != tryOnce {
					if err := sleepForRetry(ctx); err != nil {
						return nil, err
					}
				}
				continue
			}
			return nil, err
		}
		vanished = 0

		flockFn := platformFlock
		if !isBlocking {
			flockFn = platformFlockTry
		}
		if err := flockFn(fh, flockExclusive); err != nil {
			_ = fh.Close()
			if errors.Is(err, ErrLockHeld) {
				return nil, ErrLockHeld
			}
			if created {
				_ = os.Remove(path)
			}
			return nil, err
		}

		// The gate. The path must still point at the locked file.
		same, err := sameFileAtPath(path, fh)
		if err != nil {
			releaseStale(fh)
			return nil, err
		}
		if same {
			return ephemeralUnlock(fh, path), nil
		}
		releaseStale(fh)

		// The path no longer points at the locked file. Retry.
		gone := pathGone(path)
		if !gone {
			if attempt == tryOnce {
				return nil, ErrLockHeld
			}
			if err := sleepForRetry(ctx); err != nil {
				return nil, err
			}
		}
	}
	return nil, ErrLockHeld
}

// pathGone reports whether the lock file no longer exists at the path.
func pathGone(path string) bool {
	_, err := os.Stat(path)
	return err != nil && os.IsNotExist(err)
}

// sleepForRetry waits for ephemeralRetryDelay + ephemeralRetryDelayJitter. The
// function returns the context error when ctx fires first.
func sleepForRetry(ctx context.Context) error {
	d := ephemeralRetryDelay + time.Duration(rand.Int63n(int64(ephemeralRetryDelayJitter)))
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// releaseStale releases the flock and closes the descriptor of a stale
// file. The unlock comes first because POSIX record locks on AIX and
// non-illumos Solaris drop when the process closes any descriptor for that
// file.
func releaseStale(fh *os.File) {
	_ = platformUnlock(fh)
	_ = fh.Close()
}

// idempotentClose returns an unlock closure that releases the platform lock
// and closes the file descriptor. The closure is safe to call concurrently
// and more than once. A second or later call returns the error of the
// first call.
func idempotentClose(fh *os.File) func() error {
	var (
		once     sync.Once
		firstErr error
	)
	return func() error {
		once.Do(func() {
			if err := platformUnlock(fh); err != nil {
				firstErr = err
				_ = fh.Close()
				return
			}
			firstErr = fh.Close()
		})
		return firstErr
	}
}

// ephemeralUnlock removes the lock file, releases the exclusive flock, and
// closes the file descriptor. The closure is idempotent and safe to call
// concurrently. Only the first call performs the remove, unlock, and close
// sequence and reports its error. The removal comes first because the
// flock then stays the liveness signal for the next acquirer. A failed
// removal drops silently because the next holder adopts the file. The
// function returns the unlock error or the close error.
func ephemeralUnlock(fh *os.File, path string) func() error {
	var (
		once     sync.Once
		firstErr error
	)
	return func() error {
		once.Do(func() {
			_ = os.Remove(path)
			if err := platformUnlock(fh); err != nil {
				_ = fh.Close()
				firstErr = err
				return
			}
			firstErr = fh.Close()
		})
		return firstErr
	}
}
