package pepper

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const requestIDKey key = 0

type key int

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode          int
	size                int
	processingStartTime time.Time
	duration            float64
}

func Logging(logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lrw := NewLoggingResponseWriter(w)
			next.ServeHTTP(lrw, r)
			defer func() {
				requestID, ok := r.Context().Value(requestIDKey).(string)
				if !ok {
					requestID = "unknown"
				}

				ip := r.Header.Get("X-Forwarded-For")
				if ip == "" {
					ip = r.RemoteAddr
					if strings.Contains(ip, ":") {
						ip = r.RemoteAddr[:strings.IndexByte(ip, ':')]
					}
				}

				if r.URL.Path != "/ready" && r.URL.Path != "/healthz" && r.URL.Path != "/metrics" {
					logger.Println(ip, requestID, r.Method, lrw.statusCode, r.URL.RequestURI(), lrw.duration, lrw.size, r.UserAgent())
					RequestDurationGauge.WithLabelValues(strconv.Itoa(lrw.statusCode), r.Method, r.URL.Path).Set(lrw.duration)
					RequestDurationSummary.WithLabelValues(strconv.Itoa(lrw.statusCode), r.Method, r.URL.Path).Observe(lrw.duration)
				}
			}()
		})
	}
}

func NewLoggingResponseWriter(w http.ResponseWriter) *loggingResponseWriter {
	return &loggingResponseWriter{w, http.StatusOK, 0, time.Now(), 0}
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Write(body []byte) (int, error) {
	if body != nil {
		lrw.size = len(body)
	}
	code, err := lrw.ResponseWriter.Write(body)
	lrw.duration = time.Now().Sub(lrw.processingStartTime).Seconds()
	return code, err
}
