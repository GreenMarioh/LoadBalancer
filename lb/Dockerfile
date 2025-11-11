FROM golang:1.22-alpine AS build
WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN go mod tidy

RUN CGO_ENABLED=0 go build -o lb

FROM alpine:3.20
WORKDIR /app
ENV PORT=8080
COPY --from=build /app/lb /usr/local/bin/lb
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/lb"]
