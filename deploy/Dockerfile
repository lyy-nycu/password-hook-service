FROM golang:1.26.4 AS builder

WORKDIR /src
COPY go.mod ./
COPY . .
RUN go test ./... && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/password-hook-service ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/password-hook-service /password-hook-service
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/password-hook-service"]
