FROM golang:1-alpine3.23 AS builder

RUN apk add --no-cache git ca-certificates build-base

COPY . /build
WORKDIR /build
RUN ./build.sh

FROM alpine:3.23

RUN apk add --no-cache ca-certificates

COPY --from=builder /build/ai /usr/bin/ai
VOLUME /data

ENTRYPOINT ["/usr/bin/ai"]
