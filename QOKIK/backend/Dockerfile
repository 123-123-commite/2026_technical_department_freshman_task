FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/hduhelp ./cmd/api

FROM alpine:3.21
RUN adduser -D -H -u 10001 app
WORKDIR /app
COPY --from=build /out/hduhelp /app/hduhelp
COPY migrations /app/migrations
USER app
EXPOSE 8000
ENTRYPOINT ["/app/hduhelp"]
