FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o backend

FROM alpine:3.20
WORKDIR /app
ENV PORT=8081
ENV SERVICE_NAME=backend
COPY --from=build /app/backend /usr/local/bin/backend
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/backend"]
