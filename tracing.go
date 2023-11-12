package pepper

import (
	"context"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"net/http"
)

func Tracing(nextRequestID func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := r.Header.Get("X-Request-Id")
			if requestID == "" {
				requestID = nextRequestID()
			}
			ctx := context.WithValue(r.Context(), requestIDKey, requestID)
			w.Header().Set("X-Request-Id", requestID)

			if r.Method != "OPTIONS" &&
				r.Method != "HEAD" &&
				r.URL.Path != "/healthz" &&
				r.URL.Path != "/ready" &&
				r.URL.Path != "/metrics" {

				handler := otelhttp.NewHandler(next, r.Method+" "+r.URL.Path)
				handler.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
