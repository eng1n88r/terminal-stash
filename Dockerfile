# --- build stage ------------------------------------------------------------
FROM golang:1.23-alpine AS build
WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Pure-Go SQLite (modernc) → CGO disabled → fully static binary.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /stash .

# --- runtime stage ----------------------------------------------------------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget \
 && adduser -D -u 10001 app \
 && mkdir -p /data && chown app:app /data

COPY --from=build /stash /usr/local/bin/stash

USER app
ENV PORT=8080 DATA_DIR=/data
EXPOSE 8080
VOLUME /data

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8080/login >/dev/null 2>&1 || exit 1

ENTRYPOINT ["stash"]
