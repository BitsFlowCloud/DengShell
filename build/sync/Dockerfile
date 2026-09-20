FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/syncvault ./internal/syncvault
COPY internal/syncserver ./internal/syncserver
COPY internal/updateproxy ./internal/updateproxy
COPY cmd/dengshell-sync ./cmd/dengshell-sync
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /dengshell-sync ./cmd/dengshell-sync
FROM scratch
COPY --from=build /dengshell-sync /dengshell-sync
USER 65532:65532
EXPOSE 18443
ENTRYPOINT ["/dengshell-sync"]
CMD ["--data", "/data", "--ip", "127.0.0.1", "--port", "18443"]
