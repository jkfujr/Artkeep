FROM node:24-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/embed.go ./web/embed.go
COPY --from=frontend /src/web/dist/ ./web/dist/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /collection ./cmd/collection

FROM backend AS binaries
RUN mkdir -p /out/linux_amd64 /out/windows_amd64 \
    && cp /collection /out/linux_amd64/artkeep \
    && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/windows_amd64/artkeep.exe ./cmd/collection

FROM scratch AS release-binaries
COPY --from=binaries /out/ /

FROM backend AS test
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg \
    && rm -rf /var/lib/apt/lists/* \
    && go test -v ./...

FROM python:3.12-slim-bookworm AS production
ARG GALLERY_DL_VERSION=1.32.18
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && pip install --no-cache-dir gallery-dl==${GALLERY_DL_VERSION} \
    && useradd --create-home --uid 10001 archive \
    && mkdir -p /data /library/pixiv /run/collection \
    && chown archive:archive /data
COPY --from=backend /collection /usr/local/bin/collection
WORKDIR /app
ENV ARCHIVE_LISTEN=0.0.0.0:8080 ARCHIVE_DATA_DIR=/data ARCHIVE_PIXIV_DIR=/library/pixiv
USER archive
EXPOSE 8080
ENTRYPOINT ["collection"]
