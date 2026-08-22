FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /daemon ./cmd/daemon

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /daemon /usr/local/bin/daemon
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/daemon"]
