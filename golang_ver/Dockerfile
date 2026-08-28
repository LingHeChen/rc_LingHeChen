FROM golang:1.25-alpine AS builder
WORKDIR /app
ARG GOPROXY="https://proxy.golang.org|direct"
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod CGO_ENABLED=0 go build -o server ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/server /server
ENTRYPOINT ["/server"]
