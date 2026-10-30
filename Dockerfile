# syntax=docker/dockerfile:1

# 1) Build the web GUI.
FROM node:24-alpine AS web
WORKDIR /src/web
RUN npm install -g pnpm@10.34.2
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

# 2) Build the Go binary with the GUI embedded.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/switchyard ./cmd/switchyard \
    && mkdir -p /out/data

# 3) Minimal runtime image (includes CA certificates, runs as non-root).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/switchyard /switchyard
COPY --from=build --chown=65532:65532 /out/data /data
ENV PORT=8080 DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/switchyard", "healthcheck"]
ENTRYPOINT ["/switchyard"]
