package main

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

	vegeta "github.com/tsenart/vegeta/v12/lib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	lastRPS    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "bench_last_rps", Help: "Last measured requests per second"})
	lastP50    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "bench_last_latency_p50_seconds"})
	lastP90    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "bench_last_latency_p90_seconds"})
	lastP99    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "bench_last_latency_p99_seconds"})
	lastErrors = prometheus.NewGauge(prometheus.GaugeOpts{Name: "bench_last_errors_total"})
	scenarioOn = prometheus.NewGauge(prometheus.GaugeOpts{Name: "bench_scenario_running", Help: "1 while scenario is running"})
)

func init() { prometheus.MustRegister(lastRPS, lastP50, lastP90, lastP99, lastErrors, scenarioOn) }

var page = template.Must(template.New("t").Parse(`
<!doctype html><meta charset="utf-8"><title>benchctl</title>
<style>body{font-family:sans-serif;max-width:760px;margin:40px auto}input{padding:4px;margin:0 6px 6px 0}</style>
<h1>benchctl</h1>
<form action="/run" method="get">
  <label>Target URL: <input name="url" value="http://lb:8080/"></label>
  <label>Rate (req/s): <input name="rate" value="100"></label>
  <label>Duration (s): <input name="dur" value="10"></label>
  <button type="submit">Run</button>
</form>
<hr>
<h2>One-click demo scenario</h2>
<p>Warmup ➜ steady load ➜ <b>FAIL backend2</b> ➜ keep load ➜ <b>RECOVER backend2</b>.</p>
<form action="/scenario" method="post">
  <label>Warmup (s): <input name="warm" value="5"></label>
  <label>Rate 1 (rps): <input name="r1" value="150"></label>
  <label>Hold 1 (s): <input name="h1" value="10"></label>
  <label>Rate 2 (rps): <input name="r2" value="200"></label>
  <label>Hold 2 (s): <input name="h2" value="15"></label>
  <button type="submit">Run Scenario</button>
</form>
<p>Metrics: <a href="/metrics">/metrics</a></p>
`))

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { _ = page.Execute(w, nil) })
	http.HandleFunc("/run", runHandler)
	http.HandleFunc("/scenario", scenarioHandler)
	http.Handle("/metrics", promhttp.Handler())

	log.Println("benchctl listening on :7070")
	log.Fatal(http.ListenAndServe(":7070", nil))
}

func runOnce(url string, rate, seconds int) (vegeta.Metrics, error) {
	attacker := vegeta.NewAttacker()
	targeter := vegeta.NewStaticTargeter(vegeta.Target{Method: "GET", URL: url})
	var m vegeta.Metrics
	for res := range attacker.Attack(targeter, vegeta.Rate{Freq: rate, Per: time.Second}, time.Duration(seconds)*time.Second, "benchctl") {
		m.Add(res)
	}
	m.Close()
	lastRPS.Set(m.Rate)
	lastP50.Set(m.Latencies.P50.Seconds())
	lastP90.Set(m.Latencies.P90.Seconds())
	lastP99.Set(m.Latencies.P99.Seconds())
	lastErrors.Set(float64(len(m.Errors)))
	return m, nil
}

func runHandler(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	if url == "" { url = "http://lb:8080/" }
	rate, _ := strconv.Atoi(r.URL.Query().Get("rate"))
	if rate <= 0 { rate = 100 }
	dur, _ := strconv.Atoi(r.URL.Query().Get("dur"))
	if dur <= 0 { dur = 10 }

	m, _ := runOnce(url, rate, dur)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"url": url, "rate": rate, "duration_s": dur,
		"rps": m.Rate,
		"latency_p50_s": m.Latencies.P50.Seconds(),
		"latency_p90_s": m.Latencies.P90.Seconds(),
		"latency_p99_s": m.Latencies.P99.Seconds(),
		"errors_count": len(m.Errors),
	})
}

func scenarioHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Redirect(w, r, "/", http.StatusSeeOther); return }

	// read params
	geti := func(k string, def int) int {
		v, _ := strconv.Atoi(r.FormValue(k))
		if v <= 0 { return def }
		return v
	}
	warm := geti("warm", 5)
	r1 := geti("r1", 150)
	h1 := geti("h1", 10)
	r2 := geti("r2", 200)
	h2 := geti("h2", 15)

	go runScenario(warm, r1, h1, r2, h2) // async
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func runScenario(warm, r1, h1, r2, h2 int) {
	scenarioOn.Set(1)
	defer scenarioOn.Set(0)

	// 1) Warmup (low rate)
	_, _ = runOnce("http://lb:8080/", 80, warm)

	// 2) Steady load
	_, _ = runOnce("http://lb:8080/", r1, h1)

	// 3) FAIL backend2 (flip its /health down)
	_, _ = http.Get("http://backend2:8081/fail")
	time.Sleep(3 * time.Second) // let LB health check notice

	// 4) Keep higher load while backend2 is down
	_, _ = runOnce("http://lb:8080/", r2, h2)

	// 5) RECOVER backend2
	_, _ = http.Get("http://backend2:8081/recover")
	time.Sleep(4 * time.Second) // health probe to mark healthy

	// 6) Final short run to see 3-way again
	_, _ = runOnce("http://lb:8080/", r1, 8)
}
