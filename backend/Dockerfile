FROM golang:1.22-alpine AS build
WORKDIR /app

# copy go.mod first for caching
COPY go.mod ./
RUN go mod download

# now copy source & tidy (important)
COPY . .
RUN go mod tidy

RUN CGO_ENABLED=0 go build -o backend

FROM alpine:3.20
WORKDIR /app
ENV PORT=8081
ENV SERVICE_NAME=backend
COPY --from=build /app/backend /usr/local/bin/backend
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/backend"]
