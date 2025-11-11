package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"
	"sync/atomic"
	
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	start               = time.Now()
	reqCount            int64
	httpRequestsTotal   = prometheus.NewCounter(prometheus.CounterOpts{Name: "http_requests_total", Help: "Total HTTP requests"})
	httpLatencySeconds  = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "Request duration seconds", Buckets: prometheus.DefBuckets})
	httpInFlight        = prometheus.NewGauge(prometheus.GaugeOpts{Name: "http_in_flight_requests", Help: "In-flight requests"})
	unhealthy 			atomic.Bool
)

func init() {
	prometheus.MustRegister(httpRequestsTotal, httpLatencySeconds, httpInFlight)
}

func main() {
	name := env("SERVICE_NAME", "backend")
	port := env("PORT", "8081")
	jitterMs, _ := strconv.Atoi(env("LATENCY_JITTER_MS", "0"))

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
	if unhealthy.Load() {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "UNHEALTHY")
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "OK")
})
// force this instance to be unhealthy/healthy for LB health checks
mux.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
	unhealthy.Store(true)
	fmt.Fprintln(w, "backend forced UNHEALTHY")
})
mux.HandleFunc("/recover", func(w http.ResponseWriter, r *http.Request) {
	unhealthy.Store(false)
	fmt.Fprintln(w, "backend RECOVERED")
})


	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		httpInFlight.Inc()
		defer func() {
			httpInFlight.Dec()
			httpLatencySeconds.Observe(time.Since(t0).Seconds())
			httpRequestsTotal.Inc()
		}()

		reqCount++
		if jitterMs > 0 {
			time.Sleep(time.Duration(rand.Intn(jitterMs)) * time.Millisecond)
		}
		uptime := time.Since(start).Truncate(time.Second)
		host, _ := os.Hostname()
		fmt.Fprintf(w, "Hello from %s (%s)\n", name, host)
		fmt.Fprintf(w, "Requests served: %d\n", reqCount)
		fmt.Fprintf(w, "Uptime: %s\n", uptime)
		fmt.Fprintf(w, "Path: %s\n", r.URL.Path)
	})

	// expose metrics
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      logRequest(mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("Starting %s on :%s ...", name, port)
	log.Fatal(server.ListenAndServe())
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
func env(k, def string) string { if v := os.Getenv(k); v != "" { return v }; return def }
