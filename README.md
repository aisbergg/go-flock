<a name="readme-top"></a>

<div align="center">
    <picture>
        <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
        <img src="assets/logo-light.svg" alt="flock — file locking for Go" width="480">
    </picture>
    <h4>Coordinate access between processes using OS-level file locking.</h4>
    <br />
    <p>
        <a href="#"><img src="https://img.shields.io/badge/Go-%2300ADD8.svg?&logo=go&logoColor=white&style=for-the-badge" alt="GoDoc"></a>
        <a href="#"><img src="https://img.shields.io/badge/📦%20Library-00aed8?style=for-the-badge" alt="License"></a>
        <a href="https://pkg.go.dev/github.com/aisbergg/go-flock/pkg/flock"><img src="https://img.shields.io/badge/-reference-grey?logo=go&style=for-the-badge" alt="GoDoc"></a>
        <a href="https://pkg.go.dev/github.com/aisbergg/go-flock"><img src="https://img.shields.io/github/license/aisbergg/go-flock?style=for-the-badge" alt="License"></a>
        <a href="https://codecov.io/gh/aisbergg/go-flock"><img src="https://img.shields.io/codecov/c/github/aisbergg/go-flock?style=for-the-badge" alt="Coverage"></a>
        <a href="https://codecov.io/gh/aisbergg/go-flock"><img src="https://img.shields.io/github/actions/workflow/status/aisbergg/go-flock/test.yml?label=Tests&style=for-the-badge" alt="Coverage"></a>
    </p>
    <p>
        <a href="https://github.com/aisbergg/go-flock/issues/new?labels=bug&template=bug_report.yml"><img src="https://img.shields.io/badge/Report%20Bug-C2301B?style=for-the-badge" alt="Report Bug"></a>
        <a href="https://github.com/aisbergg/go-flock/issues/new?labels=enhancement&template=feature_request.yml"><img src="https://img.shields.io/badge/Request%20Feature-108C8C?style=for-the-badge" alt="Request Feature"></a>
    </p>
    <hr />
</div>

<details open="open">
  <summary>Table of Contents</summary>

- [About](#about)
- [Installation](#installation)
- [Usage](#usage)
    - [Locking and Unlocking](#locking-and-unlocking)
    - [Non-blocking Locks](#non-blocking-locks)
    - [Shared (Read) Locks](#shared-read-locks)
    - [Context-aware Locks](#context-aware-locks)
    - [Ephemeral Lock Files](#ephemeral-lock-files)
    - [File Permissions](#file-permissions)
- [Platform Notes](#platform-notes)
- [Contributing](#contributing)
- [License](#license)
- [Acknowledgements](#acknowledgements)

</details>

## About

Ever wanted to make sure only one process does something at a time? Like preventing two instances of your app from writing to the same file simultaneously? That's what `flock` does. It coordinates access between processes using files as rendezvous points.

Under the hood, `flock` uses operating-system-level advisory file locking. A lock file acts like a reservation ticket: when one process holds the lock, other processes can either wait their turn or move on if they prefer not to wait. The OS keeps track of who has the lock and enforces the rules automatically. When a process crashes or exits without unlocking, the kernel releases the lock anyway — no orphaned locks left behind.

**Features:**

- Blocking and non-blocking exclusive locks via `Lock` and `TryLock`.
- Shared (read) locks via `RLock` and `TryRLock`.
- Context-aware lock acquisition with `LockContext` and `RLockContext`.
- Idempotent unlock closures that are safe to call concurrently.
- Ephemeral lock files that remove the file on unlock and recover from a dead holder.

<p align="right"><a href="#readme-top"><b>back to top ⇧</b></a></p>

## Installation

```sh
go get -u github.com/aisbergg/go-flock
```

<p align="right"><a href="#readme-top"><b>back to top ⇧</b></a></p>

## Usage

**Synopsis:**

```go
package main

import (
    "errors"
    "fmt"

    "github.com/aisbergg/go-flock/pkg/flock"
)

func main() {
    unlock, err := flock.TryLock("/var/lock/myapp.lock")
    if err != nil {
        if errors.Is(err, flock.ErrLockHeld) {
            fmt.Println("someone else holds the lock")
            return
        }
        fmt.Printf("lock error: %v\n", err)
        return
    }

    // do work while holding the lock
    fmt.Println("locked")

    // release the lock (safe to call multiple times)
    if err := unlock(); err != nil {
        fmt.Printf("unlock error: %v\n", err)
    }
}
```

### Locking and Unlocking

Use `Lock` for a blocking exclusive lock. The returned unlock function releases the lock and closes the file descriptor. Calling unlock more than once is a no-op.

```go
unlock, err := flock.Lock("/var/lock/myapp.lock")
if err != nil {
    // handle error
}
defer unlock()

// critical section
```

### Non-blocking Locks

Use `TryLock` to attempt an exclusive lock without blocking. When the lock is unavailable, it returns `ErrLockHeld`.

```go
unlock, err := flock.TryLock("/var/lock/myapp.lock")
if err != nil {
    if errors.Is(err, flock.ErrLockHeld) {
        // another process holds the lock
    }
} else {
    defer unlock()
    // critical section
}
```

### Shared (Read) Locks

Use `RLock` for a blocking shared (read) lock. Multiple processes can hold shared locks simultaneously; exclusive locks block all other locks.

```go
unlock, err := flock.RLock("/var/lock/myapp.lock")
if err != nil {
    // handle error
}
defer unlock()

// read-only critical section
```

Use `TryRLock` for a non-blocking shared lock attempt.

### Context-aware Locks

Use `LockContext` and `RLockContext` to acquire locks with cancellation support. These functions retry until the lock is acquired or the context is cancelled.

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

unlock, err := flock.LockContext(ctx, "/var/lock/myapp.lock", 100*time.Millisecond)
if err != nil {
    // handle error (context.Canceled if timeout exceeded)
}
defer unlock()
```

The `retryDelay` parameter controls the polling interval between lock attempts. A value of zero or less uses a default of 50ms.

### Ephemeral Lock Files

By default, lock files persist on disk after unlock. Use `WithEphemeral` to remove the file when unlocking. The implementation uses an atomic `O_CREAT|O_EXCL` create. The acquirer takes an exclusive flock on the file. The holder removes the file before it releases the flock. An acquirer that finds a file without a flock adopts the file and removes it on unlock. The lock therefore recovers from a dead holder without any timeout.

```go
unlock, err := flock.Lock("/var/lock/myapp.lock", flock.WithEphemeral())
if err != nil {
    // handle error
}
defer unlock() // also removes the lock file
```

**Note:** Ephemeral mode is not compatible with shared locks (`RLock`, `TryRLock`) because a shared lock has no single creator.

### File Permissions

Use `WithPermissions` to set the file permissions when creating a new lock file. It has no effect when the file already exists. The default permission is 0600.

```go
unlock, err := flock.Lock("/var/lock/myapp.lock", flock.WithPermissions(0o644))
if err != nil {
    // handle error
}
defer unlock()
```

<p align="right"><a href="#readme-top"><b>back to top ⇧</b></a></p>

## Platform Notes

Locking behavior varies across platforms:

- **Linux, Darwin, BSDs, Illumos:** All lock modes work as documented.
- **AIX and non-illumos Solaris:** Use `fcntl F_SETLK` through `flock(3)`, which requires a writable file descriptor. The package opens these files with `O_RDWR`.
- **Windows:** Uses `LockFileEx` for exclusive locks; shared locks are not supported (an exclusive lock is taken instead).
- **Other platforms:** All lock calls return `fs.ErrUnsupported`.

<p align="right"><a href="#readme-top"><b>back to top ⇧</b></a></p>

## Contributing

If you have any suggestions, want to file a bug report or want to contribute to this project in some other way, please read the [contribution guideline](CONTRIBUTING.md).

And don't forget to give this project a star 🌟! Thanks again!

<p align="right"><a href="#readme-top"><b>back to top ⇧</b></a></p>

## License

Distributed under the BSD 3-Clause License. See `LICENSE` for more information.

<p align="right"><a href="#readme-top"><b>back to top ⇧</b></a></p>

## Acknowledgements

This library builds upon [gofrs/flock](https://github.com/gofrs/flock/), originally created by [Tim Heckman](https://github.com/theckman) and maintained by the Gofrs community. Thank you to all contributors for their work on this foundational project.

<p align="right"><a href="#readme-top"><b>back to top ⇧</b></a></p>
