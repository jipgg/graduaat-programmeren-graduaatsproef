FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod *.go ./
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /server .

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
COPY --from=builder /server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
