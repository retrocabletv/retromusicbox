# Build React frontend
FROM node:25-alpine AS frontend
WORKDIR /app/web/channel
COPY web/channel/package*.json ./
RUN npm ci
COPY web/channel/ ./
RUN npm run build

# Build Go backend
FROM golang:1.26-alpine AS backend
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /app/cmd/rmbd/static ./cmd/rmbd/static
RUN CGO_ENABLED=1 go build -o rmbd ./cmd/rmbd
RUN CGO_ENABLED=1 go build -o rmbctl ./cmd/rmbctl

# Runtime
FROM alpine:3.23
RUN apk add --no-cache ffmpeg python3 py3-pip ca-certificates && \
    pip3 install --break-system-packages yt-dlp

WORKDIR /app
COPY --from=backend /app/rmbd /app/rmbctl /app/
COPY configs/ ./configs/
COPY assets/ ./assets/

RUN mkdir -p data/cache data/ready data/thumbnails

EXPOSE 8080

CMD ["./rmbd", "--config", "configs/config.yaml"]
