FROM golang:1.24-alpine AS builder
ARG TARGETARCH
ARG TARGETVARIANT

WORKDIR /app

# Download deps first (cache layer)
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o /stb-manager .

FROM alpine:3.20
RUN apk add --no-cache dumb-init
COPY --from=builder /stb-manager /usr/local/bin/stb-manager
RUN chmod +x /usr/local/bin/stb-manager
COPY index.html /app/index.html
RUN mkdir -p /opt/ota
WORKDIR /opt/ota
EXPOSE 8080
CMD ["dumb-init", "/usr/local/bin/stb-manager"]
