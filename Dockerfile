# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO disabled: sqlite driver (modernc.org/sqlite) is pure Go, so we can
# link a fully static binary and ship it on a distroless base.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/xmail ./cmd/xmail
# distroless has no shell (can't RUN mkdir there), so pre-create the data
# dir here and COPY it over with the right ownership — the final image's
# nonroot user (uid/gid 65532) otherwise has no write access to a bare
# VOLUME mountpoint, and storage.Open fails with "unable to open database
# file (14)" (verified: this exact failure was caught by actually running
# the built image, not just by building it — see PLAN.md Fase 8 notes).
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/xmail /app/xmail
COPY --from=build --chown=nonroot:nonroot /out/data /app/data
VOLUME ["/app/data"]
ENV XMAIL_DB_PATH=/app/data/xmail.db
ENV XMAIL_LISTEN_ADDR=:5569
EXPOSE 5569
ENTRYPOINT ["/app/xmail"]
