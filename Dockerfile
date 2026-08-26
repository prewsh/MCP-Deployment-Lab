# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-deployment-controller ./cmd/server
# Distroless runs as UID/GID 65532. Prepare the database directory in the
# image so SQLite can open its default path before a persistent volume exists.
RUN mkdir -p /out-data && chown 65532:65532 /out-data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/mcp-deployment-controller /app/mcp-deployment-controller
COPY --from=build /src/web /app/web
COPY --from=build --chown=65532:65532 /out-data /data
ENV MCP_DEPLOYMENT_CONTROLLER_DB_PATH=/data/mcp-deployment-controller.db
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/mcp-deployment-controller"]
