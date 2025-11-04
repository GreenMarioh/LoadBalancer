package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"
)

var start = time.Now()
var reqCount int64

func main() {
	name := env("SERVICE_NAME", "backend")
	port := env("PORT", "8081")
	// Optional: add a bit of random latency to show retries/timeouts in LB demos
	jitterMs, _ := strconv.Atoi(env("LATENCY_JITTER_MS", "0"))

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// Report healthy if process is alive
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
