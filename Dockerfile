# Build stage
FROM golang:1.24-alpine AS builder

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
COPY *.go ./

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o sonic-siphon .

# Runtime stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache \
    ffmpeg \
    python3 \
    py3-pip \
    curl \
    nodejs \
    npm

# Install yt-dlp
RUN pip3 install --no-cache-dir --break-system-packages yt-dlp

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
