# syntax=docker/dockerfile:1
FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27.3-alpine AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/aegis ./cmd/aegis

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 aegis \
    && adduser -S -D -u 10001 -G aegis aegis
COPY --from=go-build /out/aegis /usr/local/bin/aegis
COPY --from=web-build /src/web/dist /opt/aegis/web/dist
WORKDIR /opt/aegis
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/aegis"]
CMD ["version"]
