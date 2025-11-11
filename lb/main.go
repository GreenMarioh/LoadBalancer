package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

/* ================= Metrics ================= */

var (
	lbRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "lb_requests_total", Help: "LB total handled requests"},
		[]string{"code", "method"},
	)
	lbAttemptsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "lb_backend_attempts_total", Help: "Attempts per backend"},
		[]string{"backend"},
	)
	lbFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "lb_backend_failures_total", Help: "Failures per backend"},
		[]string{"backend", "reason"},
	)
	lbLatencySeconds = prometheus.NewHistogram(
		prometheus.HistogramOpts{Name: "lb_request_duration_seconds", Help: "LB end-to-end latency", Buckets: prometheus.DefBuckets},
	)
)

func init() {
	prometheus.MustRegister(lbRequestsTotal, lbAttemptsTotal, lbFailuresTotal, lbLatencySeconds)
}

/* ================= Model ================= */

type Backend struct {
	URL            *url.URL
	Alive          bool
	ConsecFailures int
	mu             sync.RWMutex
	ReverseProxy   *httputil.ReverseProxy
	Name           string
}

func (b *Backend) SetAlive(alive bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Alive = alive
	if alive {
		b.ConsecFailures = 0
	}
}

func (b *Backend) IsAlive() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Alive
}

type LoadBalancer struct {
	Backends []*Backend
	mu       sync.Mutex
	current  int

	HealthPath      string
	HealthInterval  time.Duration
	HealthTimeout   time.Duration
	MaxConsecFail   int
	BreakerCooldown time.Duration
	ReqTimeout      time.Duration
	MaxRetries      int
}

func NewLoadBalancer(targets []string) *LoadBalancer {
	backends := make([]*Backend, 0, len(targets))
	for _, t := range targets {
		u, err := url.Parse(t)
		if err != nil {
			log.Fatalf("invalid backend url %q: %v", t, err)
		}
		proxy := httputil.NewSingleHostReverseProxy(u)
		proxy.Transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          200,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   2 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
		b := &Backend{URL: u, Alive: true, ReverseProxy: proxy, Name: u.Host}
		backends = append(backends, b)
	}
	return &LoadBalancer{
		Backends:        backends,
		HealthPath:      "/health",
		HealthInterval:  2 * time.Second,
		HealthTimeout:   1 * time.Second,
		MaxConsecFail:   3,
		BreakerCooldown: 10 * time.Second,
		ReqTimeout:      1500 * time.Millisecond,
		MaxRetries:      2,
	}
}

func (lb *LoadBalancer) nextAliveBackend() (*Backend, int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	n := len(lb.Backends)
	for i := 0; i < n; i++ {
		lb.current = (lb.current + 1) % n
		b := lb.Backends[lb.current]
		if b.IsAlive() {
			return b, lb.current, nil
		}
	}
	return nil, -1, errors.New("no alive backends")
}

/* ================= Serving (retries + metrics) ================= */

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, code: 200}

	var lastErr error
	tried := map[int]bool{}
	for attempt := 0; attempt <= lb.MaxRetries; attempt++ {
		b, idx, err := lb.nextAliveBackend()
		if err != nil {
			lastErr = err
			break
		}
		if tried[idx] {
			continue
		}
		tried[idx] = true
		lbAttemptsTotal.WithLabelValues(b.Name).Inc()

		ctx, cancel := context.WithTimeout(r.Context(), lb.ReqTimeout)
		r2 := r.Clone(ctx)
		r2.Header.Set("X-Forwarded-Host", r.Host)
		r2.Header.Set("X-Forwarded-For", clientIP(r))
		r2.Header.Set("X-Forwarded-Proto", schemeOf(r))

		b.ReverseProxy.ServeHTTP(rec, r2)
		cancel()

		// retry on timeout or 5xx
		if ctx.Err() == context.DeadlineExceeded || rec.code >= 500 {
			reason := "timeout"
			if rec.code >= 500 {
				reason = "5xx"
			}
			lbFailuresTotal.WithLabelValues(b.Name, reason).Inc()
			lb.noteFailure(b)
			continue
		}

		// success
		break
	}

	lbLatencySeconds.Observe(time.Since(start).Seconds())
	lbRequestsTotal.WithLabelValues(fmt.Sprintf("%d", rec.code), r.Method).Inc()

	if lastErr != nil {
		http.Error(w, "no upstream available", http.StatusServiceUnavailable)
	}
}

/* ================= Health checks & breaker ================= */

func (lb *LoadBalancer) noteFailure(b *Backend) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ConsecFailures++
	if b.ConsecFailures >= lb.MaxConsecFail && b.Alive {
		log.Printf("[breaker] marking %s DOWN after %d failures", b.Name, b.ConsecFailures)
		b.Alive = false
		go func(be *Backend) {
			time.Sleep(lb.BreakerCooldown)
			be.mu.Lock()
			be.Alive = true
			be.ConsecFailures = 0
			be.mu.Unlock()
			log.Printf("[breaker] cooldown over: marking %s UP (trial)", be.Name)
		}(b)
	}
}

func (lb *LoadBalancer) StartHealthChecks() {
	t := time.NewTicker(lb.HealthInterval)
	go func() {
		for range t.C {
			for _, b := range lb.Backends {
				go lb.check(b)
			}
		}
	}()
}

func (lb *LoadBalancer) check(b *Backend) {
	client := &http.Client{Timeout: lb.HealthTimeout}
	resp, err := client.Get(b.URL.String() + lb.HealthPath)
	if err != nil || resp.StatusCode != 200 {
		if err != nil {
			log.Printf("[health] %s unhealthy: %v", b.Name, err)
		} else {
			log.Printf("[health] %s unhealthy: status=%d", b.Name, resp.StatusCode)
			resp.Body.Close()
		}
		b.SetAlive(false)
		return
	}
	resp.Body.Close()
	if !b.IsAlive() {
		log.Printf("[health] %s back healthy", b.Name)
	}
	b.SetAlive(true)
}

/* ================= Helpers ================= */

func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		return r.RemoteAddr
	}
	return host
}

func schemeOf(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[LB] %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

/* ================= main ================= */

func main() {
	targetsEnv := getenv("BACKENDS", "http://backend1:8081,http://backend2:8081,http://backend3:8081")
	targets := strings.Split(targetsEnv, ",")
	for i := range targets {
		targets[i] = strings.TrimSpace(targets[i])
	}

	lb := NewLoadBalancer(targets)
	lb.StartHealthChecks()

	addr := ":" + getenv("PORT", "8080")
	log.Printf("Load Balancer listening on %s", addr)
	log.Printf("Backends: %v", targets)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", logMiddleware(lb))

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
