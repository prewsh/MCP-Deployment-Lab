# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mcp-deployment-controller ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/mcp-deployment-controller /app/mcp-deployment-controller
COPY --from=build /src/web /app/web
ENV MCP_DEPLOYMENT_CONTROLLER_DB_PATH=/data/mcp-deployment-controller.db
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/mcp-deployment-controller"]
