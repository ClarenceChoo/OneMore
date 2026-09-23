package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

type result struct {
	Duration time.Duration
	Failed   bool
}

func main() {
	endpoint := flag.String("url", "http://127.0.0.1:3000/api/plans", "feed endpoint")
	workers := flag.Int("workers", 20, "concurrent clients")
	duration := flag.Duration("duration", 30*time.Second, "test duration")
	flag.Parse()
	cookie := os.Getenv("ONEMORE_SESSION")
	if cookie == "" {
		fmt.Fprintln(os.Stderr, "set ONEMORE_SESSION to a local session cookie value")
		os.Exit(2)
	}
	if *workers < 1 || *workers > 500 || *duration < time.Second {
		fmt.Fprintln(os.Stderr, "workers must be 1–500 and duration at least one second")
		os.Exit(2)
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: *workers, MaxConnsPerHost: *workers}}
	deadline := time.Now().Add(*duration)
	started := time.Now()
	out := make(chan result, *workers*2)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(deadline) {
				req, err := http.NewRequest(http.MethodGet, *endpoint, nil)
				if err != nil {
					out <- result{Failed: true}
					return
				}
				req.AddCookie(&http.Cookie{Name: "onemore_session", Value: cookie})
				t := time.Now()
				response, err := client.Do(req)
				r := result{Duration: time.Since(t), Failed: err != nil}
				if err == nil {
					r.Failed = response.StatusCode != 200
					_ = response.Body.Close()
				}
				out <- r
			}
		}()
	}
	go func() { wg.Wait(); close(out) }()
	times := []float64{}
	failed := 0
	total := 0
	for r := range out {
		total++
		if r.Failed {
			failed++
		} else {
			times = append(times, float64(r.Duration.Microseconds())/1000)
		}
	}
	sort.Float64s(times)
	elapsed := time.Since(started).Seconds()
	percentile := func(p float64) float64 {
		if len(times) == 0 {
			return 0
		}
		i := int(float64(len(times)-1) * p)
		return times[i]
	}
	report := map[string]any{"endpoint": *endpoint, "workers": *workers, "durationSeconds": elapsed, "requests": total, "errors": failed, "requestsPerSecond": float64(total) / elapsed, "p50Ms": percentile(.50), "p95Ms": percentile(.95)}
	_ = json.NewEncoder(os.Stdout).Encode(report)
}
