//go:build !js && !wasip1

package flock_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aisbergg/go-flock/pkg/flock"
)

func ExampleLock() {
	unlock, err := flock.Lock(filepath.Join(os.TempDir(), "go-lock.lock"))
	if err != nil {
		// handle locking error
		panic(err)
	}
	defer unlock()

	fmt.Println("locked")
	// Output: locked
}

func ExampleTryLock() {
	unlock, err := flock.TryLock(filepath.Join(os.TempDir(), "go-trylock.lock"))
	if err != nil {
		if errors.Is(err, flock.ErrLockHeld) {
			fmt.Println("lock held")
			return
		}
		panic(err)
	}
	defer unlock()

	fmt.Println("locked")
	// Output: locked
}

func ExampleLockContext() {
	unlock, err := flock.LockContext(
		context.Background(),
		filepath.Join(os.TempDir(), "go-ctx.lock"),
		50*time.Millisecond,
	)
	if err != nil {
		panic(err)
	}
	defer unlock()

	fmt.Println("locked")
	// Output: locked
}
