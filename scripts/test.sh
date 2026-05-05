#!/bin/sh
# End-to-end tests. Actually downloads from YouTube via yt-dlp and re-encodes
# with ffmpeg, so the container needs network access and those binaries on PATH
# (the `test` stage of the Dockerfile installs them).
set -u

go test -count=1 -timeout 10m -v ./...
status=$?

echo
if [ "$status" -eq 0 ]; then
    echo "PASS - all tests passed"
else
    echo "FAIL - go test exited $status"
fi
exit "$status"
