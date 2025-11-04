package main

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type Backend struct {
	URL           *url.URL
	Alive         bool
	ConsecFailures int
	mu            sync.RWMutex
	ReverseProxy  *httputil.ReverseProxy
	Name          string
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
	// policy config
	HealthPath       string
	HealthInterval   time.Duration
	HealthTimeout    time.Duration
	MaxConsecFail    int
	BreakerCooldown  time.Duration
	ReqTimeout       time.Duration
	MaxRetries       int
}

func NewLoadBalancer(targets []string) *LoadBalancer {
	backends := make([]*Backend, 0, len(targets))
	for _, t := range targets {
		u, err := url.Parse(t)
		if err != nil {
			log.Fatalf("invalid backend url %q: %v", t, err)
		}
		proxy := httputil.NewSingleHostReverseProxy(u)

		// Per-request transport with timeouts
		proxy.Transport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 2 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}

		// Preserve original host? For demo it's fine to keep LB host.
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
			// Let higher level retry kick in by surfacing an error
			http.Error(w, "upstream error: "+e.Error(), http.StatusBadGateway)
		}

		b := &Backend{URL: u, Alive: true, ReverseProxy: proxy, Name: u.Host}
		backends = append(backends, b)
	}
	lb := &LoadBalancer{
		Backends:        backends,
		HealthPath:      "/health",
		HealthInterval:  2 * time.Second,
		HealthTimeout:   1 * time.Second,
		MaxConsecFail:   3,
		BreakerCooldown: 10 * time.Second,
		ReqTimeout:      1500 * time.Millisecond,
		MaxRetries:      2,
	}
	return lb
}

func (lb *LoadBalancer) nextAliveBackend() (*Backend, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	n := len(lb.Backends)
	for i := 0; i < n; i++ {
		lb.current = (lb.current + 1) % n
		b := lb.Backends[lb.current]
		if b.IsAlive() {
			return b, nil
		}
	}
	return nil, errors.New("no alive backends")
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Simple retry loop over different backends if errors occur
	var lastErr error
	tried := map[int]bool{}
	for attempt := 0; attempt <= lb.MaxRetries; attempt++ {
		b, err := lb.nextAliveBackend()
		if err != nil {
			http.Error(w, "no upstream available", http.StatusServiceUnavailable)
			return
		}
		// avoid retrying same backend twice in a row if we can
		idx := lb.current
		if tried[idx] {
			continue
		}
		tried[idx] = true

		// Per-attempt timeout
		ctx, cancel := context.WithTimeout(r.Context(), lb.ReqTimeout)
		defer cancel()

		// Copy request with context
		r2 := r.Clone(ctx)

		// Add X-Forwarded-* headers
		r2.Header.Set("X-Forwarded-Host", r.Host)
		r2.Header.Set("X-Forwarded-For", clientIP(r))
		r2.Header.Set("X-Forwarded-Proto", schemeOf(r))

		rec := &statusRecorder{ResponseWriter: w, code: 200}
		b.ReverseProxy.ServeHTTP(rec, r2)

		if ctx.Err() == context.DeadlineExceeded || rec.code >= 500 {
			lastErr = errors.New("upstream timeout or 5xx")
			lb.noteFailure(b)
			continue
		}

		// success path
		return
	}
	// failed all attempts
	http.Error(w, "all upstreams failed: "+lastErr.Error(), http.StatusBadGateway)
}

func (lb *LoadBalancer) noteFailure(b *Backend) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ConsecFailures++
	if b.ConsecFailures >= lb.MaxConsecFail {
		if b.Alive {
			log.Printf("[breaker] Marking %s DOWN after %d failures", b.Name, b.ConsecFailures)
		}
		b.Alive = false
		// Cooldown re-enable later
		go func(be *Backend) {
			time.Sleep(lb.BreakerCooldown)
			be.mu.Lock()
			be.Alive = true
			be.ConsecFailures = 0
			be.mu.Unlock()
			log.Printf("[breaker] Cooldown over: marking %s UP (trial)", be.Name)
		}(b)
	}
}

// background health checker
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
		// mark failure softly (without tripping breaker immediately)
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
		log.Printf("[health] %s back to healthy", b.Name)
	}
	b.SetAlive(true)
}

func clientIP(r *http.Request) string {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		parts := strings.Split(r.RemoteAddr, ":")
		ip = parts[0]
	}
	return ip
}

func schemeOf(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func main() {
	targetsEnv := env("BACKENDS", "http://backend1:8081,http://backend2:8081,http://backend3:8081")
	targets := strings.Split(targetsEnv, ",")
	for i := range targets {
		targets[i] = strings.TrimSpace(targets[i])
	}

	rand.Seed(time.Now().UnixNano())
	lb := NewLoadBalancer(targets)
	lb.StartHealthChecks()

	addr := ":" + env("PORT", "8080")
	log.Printf("Load Balancer listening on %s", addr)
	log.Printf("Backends: %v", targets)

	srv := &http.Server{
		Addr:         addr,
		Handler:      logMiddleware(lb),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[LB] %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
