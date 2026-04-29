FROM golang:1.22-alpine AS builder
WORKDIR /src

# Copy workspace and all modules
COPY go.work go.work.sum* ./
COPY cache-node/ ./cache-node/
COPY router/ ./router/
COPY cache-client/ ./cache-client/
COPY invalidation-service/ ./invalidation-service/
COPY example-app/ ./example-app/

ARG SERVICE
RUN cd $SERVICE && go build -o /app .

FROM alpine:3.19
COPY --from=builder /app /app
ENTRYPOINT ["/app"]
