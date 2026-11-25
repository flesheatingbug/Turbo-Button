FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" -o /turbo-relay .

FROM scratch
COPY --from=build /turbo-relay /turbo-relay
ENV CONFIG_DIR=/config LISTEN=:8080 TRUST_PROXY=1
EXPOSE 8080
VOLUME /config
ENTRYPOINT ["/turbo-relay"]
