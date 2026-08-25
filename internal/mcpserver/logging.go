package mcpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// requestLogger logs only MCP routing metadata. It intentionally does not log
// authorization headers, cookies, request bodies, or tool arguments.
func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		return next
	}

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: response}
		next.ServeHTTP(recorder, request)

		attributes := []slog.Attr{
			slog.String("http_method", request.Method),
			slog.Int("status", recorder.statusCode()),
			slog.Duration("duration", time.Since(started)),
		}
		if method := request.Header.Get("Mcp-Method"); method != "" {
			attributes = append(attributes, slog.String("mcp_method", method))
		}
		if name := request.Header.Get("Mcp-Name"); name != "" {
			attributes = append(attributes, slog.String("tool_name", name))
		}
		if version := request.Header.Get("Mcp-Protocol-Version"); version != "" {
			attributes = append(attributes, slog.String("protocol_version", version))
		}

		logger.LogAttrs(request.Context(), slog.LevelInfo, "MCP HTTP request completed", attributes...)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *responseRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(body)
}

func (recorder *responseRecorder) Flush() {
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (recorder *responseRecorder) statusCode() int {
	if recorder.status == 0 {
		return http.StatusOK
	}
	return recorder.status
}

func capabilitiesAsMap(capabilities any) map[string]any {
	if capabilities == nil {
		return nil
	}

	encoded, err := json.Marshal(capabilities)
	if err != nil {
		return nil
	}

	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil
	}
	return result
}
