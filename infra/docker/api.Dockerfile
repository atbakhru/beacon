FROM golang:1.27.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=secret,id=proxy_ca,target=/usr/local/share/ca-certificates/session.crt sh -c 'if [ -f /usr/local/share/ca-certificates/session.crt ]; then update-ca-certificates; fi; go mod download'
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /beacon ./cmd/beacon
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /beacon /app/beacon
COPY db /app/db
EXPOSE 8080
ENTRYPOINT ["/app/beacon"]
