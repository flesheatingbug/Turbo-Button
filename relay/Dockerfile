FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
ARG VERSION=dev TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" -o /turbo-relay .

FROM scratch
COPY --from=build /turbo-relay /turbo-relay
# CA certificates so the "Download Turbo Button" button can fetch the latest build from GitHub.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV CONFIG_DIR=/config LISTEN=:8080 TRUST_PROXY=1
EXPOSE 8080
VOLUME /config
ENTRYPOINT ["/turbo-relay"]
