package core

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

var latencyBounds = [...]time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, time.Second, 2500 * time.Millisecond, 5 * time.Second, 10 * time.Second}

type opsMetrics struct {
	startedAt    time.Time
	requests     atomic.Uint64
	clientErrors atomic.Uint64
	serverErrors atomic.Uint64
	latency      [len(latencyBounds) + 1]atomic.Uint64
}

func (m *opsMetrics) observe(duration time.Duration, status int) {
	m.requests.Add(1)
	if status >= 500 {
		m.serverErrors.Add(1)
	} else if status >= 400 {
		m.clientErrors.Add(1)
	}
	for i, bound := range latencyBounds {
		if duration <= bound {
			m.latency[i].Add(1)
			return
		}
	}
	m.latency[len(latencyBounds)].Add(1)
}

func (m *opsMetrics) percentile(p float64) float64 {
	total := m.requests.Load()
	if total == 0 {
		return 0
	}
	target := uint64(float64(total)*p + .999999)
	var cumulative uint64
	for i, bound := range latencyBounds {
		cumulative += m.latency[i].Load()
		if cumulative >= target {
			return float64(bound.Milliseconds())
		}
	}
	return 10000
}

type opsSnapshot struct {
	Since                  time.Time `json:"since"`
	APIRequests            uint64    `json:"apiRequests"`
	APIClientErrors        uint64    `json:"apiClientErrors"`
	APIServerErrors        uint64    `json:"apiServerErrors"`
	APILatencyP95Ms        float64   `json:"apiLatencyP95Ms"`
	APILatencyP99Ms        float64   `json:"apiLatencyP99Ms"`
	DBConnectionsTotal     int32     `json:"dbConnectionsTotal"`
	DBConnectionsAcquired  int32     `json:"dbConnectionsAcquired"`
	DBConnectionsIdle      int32     `json:"dbConnectionsIdle"`
	PendingOutbox          int64     `json:"pendingOutbox"`
	OldestPendingSeconds   float64   `json:"oldestPendingSeconds"`
	NotificationP95Seconds float64   `json:"notificationP95Seconds"`
}

func (m *opsMetrics) snapshot(ctx context.Context, s *Store) (opsSnapshot, error) {
	stats := s.DB.Stat()
	out := opsSnapshot{
		Since:                 m.startedAt,
		APIRequests:           m.requests.Load(),
		APIClientErrors:       m.clientErrors.Load(),
		APIServerErrors:       m.serverErrors.Load(),
		APILatencyP95Ms:       m.percentile(.95),
		APILatencyP99Ms:       m.percentile(.99),
		DBConnectionsTotal:    stats.TotalConns(),
		DBConnectionsAcquired: stats.AcquiredConns(),
		DBConnectionsIdle:     stats.IdleConns(),
	}
	err := s.DB.QueryRow(ctx, `SELECT count(*)::bigint,coalesce(extract(epoch from(clock_timestamp()-min(created_at))),0)::float8 FROM outbox_events WHERE delivered_at IS NULL`).Scan(&out.PendingOutbox, &out.OldestPendingSeconds)
	if err != nil {
		return out, err
	}
	err = s.DB.QueryRow(ctx, `SELECT coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch from(n.created_at-e.created_at))),0)::float8
FROM notifications n JOIN outbox_events e ON e.id=n.event_id WHERE n.created_at>clock_timestamp()-interval '24 hours'`).Scan(&out.NotificationP95Seconds)
	return out, err
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

const opsHTML = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>OneMore operations</title>
<style>body{font:15px system-ui,sans-serif;background:#f7f9fc;color:#17243e;margin:0;padding:32px}main{max-width:900px;margin:auto}h1{font-size:30px;letter-spacing:-.05em;margin:0 0 8px}p{color:#66758f;line-height:1.5}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:12px;margin-top:28px}.card{background:#fff;border:1px solid #e2e8f1;border-radius:12px;padding:20px}.label{font-size:12px;color:#73819a;font-weight:700}.value{font-size:28px;font-weight:800;margin-top:8px;letter-spacing:-.05em}.detail{font-size:12px;color:#71809a;margin-top:5px}.warn{color:#a74e40}footer{font-size:12px;color:#8b98aa;margin-top:28px}</style>
<main><h1>OneMore operations</h1><p>Live measurements from this API process and the shared database. Latency percentiles are approximate histogram bounds since startup.</p><div id="grid" class="grid" aria-live="polite">Loading…</div><footer id="updated"></footer></main>
<script>const cards=[['API latency','apiLatencyP95Ms','ms p95',v=>Math.round(v)],['API errors','apiServerErrors','5xx since startup',v=>v],['API requests','apiRequests','since startup',v=>v],['DB connections','dbConnectionsAcquired','acquired now',v=>v],['Pending deliveries','pendingOutbox','outbox events',v=>v],['Oldest pending','oldestPendingSeconds','seconds',v=>Math.round(v)],['Notification delay','notificationP95Seconds','seconds p95, 24h',v=>Math.round(v*10)/10]];
async function refresh(){try{const response=await fetch('/internal/ops.json',{cache:'no-store'});if(!response.ok)throw Error('Unable to load metrics');const data=await response.json();document.getElementById('grid').innerHTML='';for(const [label,key,unit,format] of cards){const card=document.createElement('div');card.className='card';const title=document.createElement('div');title.className='label';title.textContent=label;const value=document.createElement('div');value.className='value'+((key==='pendingOutbox'||key==='apiServerErrors')&&data[key]>0?' warn':'');value.textContent=format(data[key]);const detail=document.createElement('div');detail.className='detail';detail.textContent=unit;card.append(title,value,detail);document.getElementById('grid').append(card)}document.getElementById('updated').textContent='Updated '+new Date().toLocaleTimeString()+' · API p99 '+Math.round(data.apiLatencyP99Ms)+' ms · '+data.apiClientErrors+' client errors · DB '+data.dbConnectionsTotal+' total / '+data.dbConnectionsIdle+' idle'}catch(error){document.getElementById('updated').textContent=error.message}}
refresh();setInterval(refresh,5000)</script></html>`

func (m *opsMetrics) servePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprint(w, opsHTML)
}
