FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o media-engine cmd/media-engine/main.go
RUN CGO_ENABLED=0 GOOS=linux go build -o llm-orchestrator-server cmd/llm-orchestrator-server/main.go

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/media-engine .
COPY --from=builder /app/llm-orchestrator-server .
COPY --from=builder /app/frontend ./frontend

# Expose default ports
EXPOSE 8080 8081

CMD ["./media-engine"]
