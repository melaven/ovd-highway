# syntax=docker/dockerfile:1

# Stage 1: build the Control Plane SPA (Vite -> ui/dist).
FROM node:20-alpine AS ui-build
WORKDIR /app/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Stage 2: compile the engine as a static Linux binary.
FROM golang:1.27.4-alpine AS go-build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-build /app/ui/dist ./ui/dist
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/engine ./cmd/engine

# Stage 3: minimal runtime image. /data is the working directory so the
# MTProto session.json lands on the mounted volume and survives rebuilds.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app \
    && mkdir -p /data \
    && chown app:app /data
WORKDIR /data
COPY --from=go-build /out/engine /app/engine
USER app
EXPOSE 8080
ENTRYPOINT ["/app/engine"]
