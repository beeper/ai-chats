FROM golang:1.25-alpine3.23 AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

RUN apk add --no-cache ca-certificates git build-base

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH \
	go build \
		-tags goolm \
		-trimpath \
		-ldflags "-s -w -X main.Tag=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
		-o /out/agentremote \
		./cmd/agentremote

FROM alpine:3.23

RUN apk add --no-cache bash ca-certificates curl jq yq-go

ENV HOME=/data

WORKDIR /data
VOLUME /data

COPY --from=builder /out/agentremote /usr/local/bin/agentremote

ENTRYPOINT ["/usr/local/bin/agentremote"]
CMD ["help"]
