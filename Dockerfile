# syntax=docker/dockerfile:1
FROM debian:bookworm-slim AS build

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git \
    && rm -rf /var/lib/apt/lists/*

ENV MISE_VERSION=v2026.8.9
RUN curl -fsSL https://mise.run | sh
ENV PATH="/root/.local/bin:${PATH}"

WORKDIR /src
COPY mise.toml package.json aube-lock.yaml go.mod go.sum ./
RUN mise trust mise.toml \
    && mise install go@1.25.13 node@22.23.2 aube@1.41.0 \
    && mise exec -- aube install --frozen-lockfile \
    && mise exec -- go mod download

COPY . .
RUN mise exec -- aube run build \
    && CGO_ENABLED=0 mise exec -- go build -trimpath -ldflags='-s -w' -o /out/nimotsu ./cmd/nimotsu \
    && mkdir -p /out/data \
    && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/nimotsu /app/nimotsu
COPY --from=build /src/web/dist /app/web
COPY --from=build --chown=65532:65532 /out/data /data

ENV NIMOTSU_LISTEN=:8080 \
    NIMOTSU_DATA_PATH=/data/nimotsu.db \
    NIMOTSU_WEB_DIR=/app/web
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/app/nimotsu"]
