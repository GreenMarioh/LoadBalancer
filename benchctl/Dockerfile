FROM golang:1.22-alpine AS build
WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN go mod tidy

RUN CGO_ENABLED=0 go build -o benchctl

FROM alpine:3.20
WORKDIR /app
EXPOSE 7070
COPY --from=build /app/benchctl /usr/local/bin/benchctl
ENTRYPOINT ["/usr/local/bin/benchctl"]
