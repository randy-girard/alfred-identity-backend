FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /daemon ./cmd/daemon

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /daemon /app/bin/daemon
COPY Procfile /app/Procfile
ENV PATH="/app/bin:${PATH}"
EXPOSE 8080
# Compose uses this combined process. Dokku runs Procfile lines instead.
# No ENTRYPOINT: Procfile commands must be the full command, not extra args.
CMD ["/app/bin/daemon", "-process", "all"]
