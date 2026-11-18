# Stage 1 (Build)
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS builder

RUN apk add --update --no-cache git make mailcap
WORKDIR /app/
COPY go.mod go.sum /app/
RUN go mod download
COPY . /app/
ARG VERSION
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -ldflags="-s -w -X github.com/Aquadactyl/manta/system.Version=$VERSION" \
    -v \
    -trimpath \
    -o manta \
    wings.go
RUN echo "ID=\"distroless\"" > /etc/os-release

# Stage 2 (Final)
FROM gcr.io/distroless/static:latest
COPY --from=builder /etc/os-release /etc/os-release
COPY --from=builder /etc/mime.types /etc/mime.types

COPY --from=builder /app/manta /usr/bin/manta
# Compatibility executable for existing container commands.
COPY --from=builder /app/manta /usr/bin/wings

ENTRYPOINT ["/usr/bin/manta"]
CMD []

EXPOSE 8080 2022
