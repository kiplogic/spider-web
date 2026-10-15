# Build stage
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags '-w -s' -o /spider-web .

# Run stage (kecil, hanya binary + CA certs untuk HTTPS)
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=build /spider-web /spider-web
EXPOSE 8080
ENV PORT=8080
ENTRYPOINT ["/spider-web"]
