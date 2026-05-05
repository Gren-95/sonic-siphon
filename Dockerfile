# Build stage
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod ./
COPY go.sum* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o sonic-siphon ./cmd/server

# Runtime stage
FROM alpine:latest AS runtime

# Install runtime dependencies
RUN apk add --no-cache \
    ffmpeg \
    python3 \
    py3-pip \
    curl \
    nodejs \
    npm

# Install yt-dlp.
# Pinned for reproducibility — when YouTube ships a breaking change yt-dlp will
# release a fix, and you'll need to bump this version (and the matching one in
# the test stage below) to pick it up. Floating to "latest" gives surprise
# upgrades on every rebuild and makes diagnosis harder.
RUN pip3 install --no-cache-dir --break-system-packages yt-dlp==2026.3.17

# Set working directory
WORKDIR /app

# Copy built binary from builder
COPY --from=builder /app/sonic-siphon .

# Copy package files for npm
COPY package.json tailwind.config.js ./

# Install npm dependencies
RUN npm install

# Copy application files
COPY templates/ templates/
COPY static/ static/

# Build Tailwind CSS
RUN npm run build:css

# Create temp and output directories
RUN mkdir -p /temp /output

# Expose port 5000
EXPOSE 5000

# Run the application
CMD ["./sonic-siphon"]

# ---------------------------------------------------------------------------
# Test stage — used by `docker compose --profile test run --rm test ...`.
# Carries the Go toolchain plus the same external tools the integration tests
# shell out to (yt-dlp, ffmpeg, ffprobe) so test runs don't reinstall them
# on every invocation. The project source is mounted at runtime; nothing is
# COPY'd here.
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS test
RUN apk add --no-cache ffmpeg python3 py3-pip gcc musl-dev \
    && pip3 install --no-cache-dir --break-system-packages yt-dlp==2026.3.17
WORKDIR /app
ENV GOPATH=/app/.gocache GOCACHE=/app/.gocache/build CGO_ENABLED=1
CMD ["go", "test", "./..."]
