FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/clipd ./cmd/clipd

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -S clip && adduser -S -G clip clip
COPY --from=build /out/clipd /usr/local/bin/clipd
USER clip
ENV CLIP_HTTP_ADDR=0.0.0.0:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/usr/local/bin/clipd", "-healthcheck"]
ENTRYPOINT ["/usr/local/bin/clipd"]
