FROM node:22-alpine AS frontend
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build
FROM golang:1.24-alpine AS backend
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go store.go ./
RUN CGO_ENABLED=0 go build -o /one-more-pod .
FROM alpine:3.21
WORKDIR /app
COPY --from=backend /one-more-pod /app/one-more-pod
COPY --from=frontend /app/web/dist /app/web/dist
USER 65532:65532
EXPOSE 8080
CMD ["/app/one-more-pod"]
