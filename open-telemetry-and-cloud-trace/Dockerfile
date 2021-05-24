FROM golang:1.15-alpine as builder

WORKDIR /app

COPY go.* ./
RUN go mod download

COPY . ./
RUN go build -o /bin/app

FROM alpine:latest

COPY --from=builder /bin/app /bin/app

ENTRYPOINT ["/bin/app"]
