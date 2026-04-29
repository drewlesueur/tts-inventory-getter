FROM golang:1.22-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/scraper ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y ca-certificates chromium && rm -rf /var/lib/apt/lists/*
ENV CHROME_PATH=/usr/bin/chromium
WORKDIR /app
COPY --from=builder /bin/scraper /app/scraper
COPY configs /app/configs
EXPOSE 8080
CMD ["/app/scraper"]
