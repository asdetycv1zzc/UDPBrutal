FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o /udpbrutal ./cmd/udpbrutal

FROM alpine:3.22
COPY --from=builder /udpbrutal /usr/local/bin/udpbrutal
COPY examples/server.yaml /etc/udpbrutal/config.yaml
EXPOSE 443/udp
ENTRYPOINT ["/usr/local/bin/udpbrutal"]
CMD ["server", "-c", "/etc/udpbrutal/config.yaml"]
