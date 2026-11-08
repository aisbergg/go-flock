//go:build !js && !wasip1

package flock_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisbergg/go-flock/internal/lib/test/assert"
	"github.com/aisbergg/go-flock/internal/lib/test/require"
	"github.com/aisbergg/go-flock/pkg/flock"
)

func TestLock_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	unlock, err := flock.Lock(path)
	require.NoError(t, err)
	require.NotNil(t, unlock)

	require.NoError(t, unlock())

	// Re-acquire after release.
	unlock2, err := flock.Lock(path)
	require.NoError(t, err)
	require.NoError(t, unlock2())
}

func TestUnlock_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	unlock, err := flock.Lock(path)
	require.NoError(t, err)

	require.NoError(t, unlock())
	require.NoError(t, unlock())
	require.NoError(t, unlock())
}

func TestTryLock_Exclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	unlock, err := flock.TryLock(path)
	require.NoError(t, err)
	require.NotNil(t, unlock)
	defer unlock()

	_, err = flock.TryLock(path)
	require.ErrorIs(t, err, flock.ErrLockHeld)
}

func TestRLock_Shared(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	a, err := flock.RLock(path)
	require.NoError(t, err)
	defer a()

	// In most Unix variants two shared locks on the same FD-inode coexist;
	// on AIX/Solaris they do not. Either way the call must not error.
	b, err := flock.TryRLock(path)
	if err != nil {
		require.ErrorIs(t, err, flock.ErrLockHeld)
	} else {
		require.NoError(t, b())
	}
}

func TestTryRLock_BlockedByExclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	a, err := flock.Lock(path)
	require.NoError(t, err)
	defer a()

	_, err = flock.TryRLock(path)
	require.ErrorIs(t, err, flock.ErrLockHeld)
}

func TestLockContext_Cancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	holder, err := flock.Lock(path)
	require.NoError(t, err)
	defer holder()

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel while the lock is taken, so the context-bound retry observes
	// context.Done() before the lock frees up.
	done := make(chan struct{})
	var gotErr error
	go func() {
		_, gotErr = flock.LockContext(ctx, path, 5*time.Millisecond)
		close(done)
	}()

	// Give the goroutine a chance to enter the retry loop.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("LockContext did not return after cancel")
	}

	require.ErrorIs(t, gotErr, context.Canceled)
}

func TestLockContext_TimesOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	holder, err := flock.Lock(path)
	require.NoError(t, err)
	defer holder()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err = flock.LockContext(ctx, path, 5*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWithPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	_, err := flock.Lock(path, flock.WithPermissions(0o644))
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0o644))
}

func TestEphemeral_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	unlock, err := flock.Lock(path, flock.WithEphemeral())
	require.NoError(t, err)

	_, statErr := os.Stat(path)
	require.NoError(t, statErr)

	require.NoError(t, unlock())
	_, statErr = os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "ephemeral lock file should be removed on unlock")
}

func TestEphemeral_FileGoneBetweenHolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	first, err := flock.Lock(path, flock.WithEphemeral())
	require.NoError(t, err)
	require.NoError(t, first())

	// File should be gone now.
	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr), "file should be missing after first unlock")

	second, err := flock.Lock(path, flock.WithEphemeral())
	require.NoError(t, err)
	require.NoError(t, second())
}

func TestEphemeral_UniqueCreator(t *testing.T) {
	// Stress test: many goroutines contend on a single ephemeral path. The
	// property we assert is that lock-acquisition serializes: at most one
	// goroutine observes success between any two unlock events.
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	const goroutines = 32
	const iterations = 50

	missingObserved := atomic.Int64{}
	var holders atomic.Int64
	var maxConcurrent atomic.Int64

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				unlock, err := flock.TryLock(path, flock.WithEphemeral())
				if err != nil {
					if errors.Is(err, flock.ErrLockHeld) {
						continue
					}
					t.Errorf("unexpected lock error: %v", err)
					return
				}

				// While holding the lock, the file must exist.
				if _, statErr := os.Stat(path); statErr != nil {
					t.Errorf("expected file to exist while held: %v", statErr)
				}

				cur := holders.Add(1)
				for {
					old := maxConcurrent.Load()
					if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
						break
					}
				}
				holders.Add(-1)

				if err := unlock(); err != nil {
					t.Errorf("unlock error: %v", err)
				}

				// After unlock, the file is gone (or about to be removed).
				_, statErr := os.Stat(path)
				if statErr == nil {
					continue
				}
				if os.IsNotExist(statErr) {
					missingObserved.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, maxConcurrent.Load(), int64(1), "at most one process should hold the lock at any time")
	assert.True(t, missingObserved.Load() > 0, "we should observe the file missing between holders")
}

// TestEphemeral_AdoptsOrphanFile checks the recovery from a dead holder.
// The dead holder leaves the file on disk with no flock held. The next
// acquirer must adopt the file instead of spinning forever.
func TestEphemeral_AdoptsOrphanFile(t *testing.T) {
	t.Run("Lock", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "lock.file")

		// The exact state that a dead creator or a dead holder leaves.
		require.NoError(t, os.WriteFile(path, nil, 0o600))

		type result struct {
			unlock func() error
			err    error
		}
		ch := make(chan result, 1)
		go func() {
			unlock, err := flock.Lock(path, flock.WithEphemeral())
			ch <- result{unlock: unlock, err: err}
		}()

		var res result
		select {
		case res = <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("Lock did not return for an orphaned lock file")
		}
		require.NoError(t, res.err)
		require.NotNil(t, res.unlock)
		require.NoError(t, res.unlock())

		_, statErr := os.Stat(path)
		require.True(t, os.IsNotExist(statErr), "ephemeral lock file should be removed on unlock")
	})

	t.Run("LockContext", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "lock.file")

		require.NoError(t, os.WriteFile(path, nil, 0o600))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		unlock, err := flock.LockContext(ctx, path, 5*time.Millisecond, flock.WithEphemeral())
		require.NoError(t, err)
		require.NotNil(t, unlock)
		require.NoError(t, unlock())

		_, statErr := os.Stat(path)
		require.True(t, os.IsNotExist(statErr), "ephemeral lock file should be removed on unlock")
	})

	t.Run("LockContextCancel", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "lock.file")

		holder, err := flock.Lock(path, flock.WithEphemeral())
		require.NoError(t, err)
		defer holder()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err = flock.LockContext(ctx, path, 5*time.Millisecond, flock.WithEphemeral())
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

// TestEphemeral_BlockingSingleHolder runs the blocking mode under stress.
// The lock must serialize the holders, and the file must exist while a
// holder keeps it.
func TestEphemeral_BlockingSingleHolder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	const goroutines = 16
	const iterations = 20

	var holders atomic.Int64
	var maxConcurrent atomic.Int64

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				unlock, err := flock.Lock(path, flock.WithEphemeral())
				if err != nil {
					t.Errorf("unexpected lock error: %v", err)
					return
				}

				// While holding the lock, the file must exist.
				if _, statErr := os.Stat(path); statErr != nil {
					t.Errorf("expected file to exist while held: %v", statErr)
				}

				cur := holders.Add(1)
				for {
					old := maxConcurrent.Load()
					if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
						break
					}
				}
				holders.Add(-1)

				if err := unlock(); err != nil {
					t.Errorf("unlock error: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, maxConcurrent.Load(), int64(1), "at most one holder should hold the lock at any time")
}

func TestRLock_WithEphemeralRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	_, err := flock.RLock(path, flock.WithEphemeral())
	require.Error(t, err)
	require.ErrorContains(t, err, "WithEphemeral is not supported")

	_, err = flock.TryRLock(path, flock.WithEphemeral())
	require.Error(t, err)
	require.ErrorContains(t, err, "WithEphemeral is not supported")

	_, err = flock.RLockContext(context.Background(), path, time.Millisecond, flock.WithEphemeral())
	require.Error(t, err)
	require.ErrorContains(t, err, "WithEphemeral is not supported")
}

func TestStat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lock.file")

	_, err := flock.Stat(path)
	require.True(t, os.IsNotExist(err))

	_, err = flock.Lock(path)
	require.NoError(t, err)
	info, err := flock.Stat(path)
	require.NoError(t, err)
	assert.NotNil(t, info)
}

func TestUnlock_GoroutineSafe(t *testing.T) {
	dir := t.TempDir()

	t.Run("traditional", func(t *testing.T) {
		path := filepath.Join(dir, "trad.lock")
		unlock, err := flock.Lock(path)
		require.NoError(t, err)

		const n = 16
		var wg sync.WaitGroup
		errs := make([]error, n)
		wg.Add(n)
		for i := range n {
			go func(i int) {
				defer wg.Done()
				errs[i] = unlock()
			}(i)
		}
		wg.Wait()

		// Idempotent under concurrent calls: no panic, no goroutine
		// blocks indefinitely. File still exists for the traditional mode.
		for _, e := range errs {
			_ = e // tolerated nil; first caller is the only real one
		}
		_, err = flock.Stat(path)
		require.NoError(t, err, "traditional lock should leave the file in place")
	})

	t.Run("ephemeral", func(t *testing.T) {
		path := filepath.Join(dir, "eph.lock")
		unlock, err := flock.Lock(path, flock.WithEphemeral())
		require.NoError(t, err)

		const n = 16
		var wg sync.WaitGroup
		wg.Add(n)
		for range n {
			go func() {
				defer wg.Done()
				_ = unlock()
			}()
		}
		wg.Wait()

		_, err = flock.Stat(path)
		require.True(t, os.IsNotExist(err))
	})
}
