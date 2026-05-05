#!/bin/sh
# Same end-to-end suite, with the Go race detector enabled.
set -u

go test -race -count=1 -timeout 15m -v ./...
status=$?

echo
if [ "$status" -eq 0 ]; then
    echo "PASS - all tests passed (race detector clean)"
else
    echo "FAIL - go test -race exited $status"
fi
exit "$status"
