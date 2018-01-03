
FROM golang:1.9.2-alpine AS builder

WORKDIR /go/src/app

COPY . .

RUN go install



FROM alpine:3.6

WORKDIR /opt

COPY --from=builder /go/bin/app .

ENTRYPOINT ["/opt/app"]
