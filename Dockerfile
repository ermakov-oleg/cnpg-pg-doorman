FROM golang:1.26 AS builder

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /plugin ./main.go

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /plugin /plugin
USER 10001:10001
ENTRYPOINT ["/plugin"]
