# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /gpu-scheduler ./cmd/scheduler

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Copy binary from builder
COPY --from=builder /gpu-scheduler .

# Copy config
COPY config.yaml .

# Expose ports
EXPOSE 8888 9090

# Run
ENTRYPOINT ["./gpu-scheduler"]