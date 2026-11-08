#!/bin/bash
# Usage: ./run.sh [number-of-instances]

set -e

NUM=${1:-5}

echo "Starting $NUM instances of flock-playground..."
echo ""

PIDS=()
for i in $(seq 1 "$NUM"); do
    go run main.go "$i" &
    PIDS+=($!)
done

echo "All instances started (PIDs: ${PIDS[*]})"
echo ""

for pid in "${PIDS[@]}"; do
    wait "$pid"
done

echo ""
echo "All instances finished."
