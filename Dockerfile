FROM golang:latest as builder

WORKDIR /app
COPY . .
RUN go mod download -x
RUN GOOS=linux GOARCH=amd64 go build -o /app/greenlight ./cmd/api

FROM alpine:latest

WORKDIR /bin

COPY --from=builder /app/greenlight /bin/greenlight

ENTRYPOINT [ "/bin/greenlight" ]
