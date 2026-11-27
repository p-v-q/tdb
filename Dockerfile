# syntax=docker/dockerfile:1
#
# Multi-arch image for TDB. The build stage runs on the builder's native
# platform and cross-compiles with GOOS/GOARCH, so arm64 images do not need a
# slow emulated Go build.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -o /out/tdb ./cmd/tdb

# A small runtime with CA certificates (database TLS) and a writable home for
# settings/history. The TDB binary itself is static and needs no libc.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -h /home/tdb tdb
COPY --from=build /out/tdb /usr/local/bin/tdb

USER tdb
WORKDIR /home/tdb
ENV TERM=xterm-256color \
    HOME=/home/tdb

ENTRYPOINT ["/usr/local/bin/tdb"]
