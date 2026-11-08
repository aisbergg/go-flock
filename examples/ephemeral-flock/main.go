package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/aisbergg/go-flock/pkg/flock"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: flock-playground <instance-number>")
		os.Exit(1)
	}

	instance, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Printf("error: invalid instance number %q\n", os.Args[1])
		os.Exit(1)
	}

	lockPath := "/tmp/flock-playground.lock"

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Printf("[%d] trying to acquire lock...\n", instance)

	unlock, err := flock.LockContext(ctx, lockPath, 100*time.Millisecond, flock.WithEphemeral())
	if err != nil {
		fmt.Printf("[%d] failed to acquire lock: %v\n", instance, err)
		os.Exit(1)
	}

	fmt.Printf("[%d] GOT THE LOCK!\n", instance)

	time.Sleep(1 * time.Second)

	if err := unlock(); err != nil {
		fmt.Printf("[%d] error releasing lock: %v\n", instance, err)
	} else {
		fmt.Printf("[%d] released lock\n", instance)
	}
}
