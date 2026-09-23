# Feed benchmark baseline

Measured on 23 September 2026 with the local Docker Compose stack on an arm64 host. Docker had 10 CPUs and 8.22 GB of memory available. The dataset contained one active plan and three test profiles. The client requested `GET /api/plans` with a valid local session; the five-second shared feed-ID cache was enabled. Twenty concurrent clients ran for ten seconds through Nginx on `127.0.0.1:3000`.

| Metric | Result |
| --- | ---: |
| Requests | 35,025 |
| HTTP 200 responses | 35,025 |
| Errors | 0 |
| Achieved throughput | 3,500.6 requests/s |
| p50 latency | 4.15 ms |
| p95 latency | 14.54 ms |

The load tool reads each response body before reusing the connection. An earlier trial that closed bodies without draining them created transport errors in the load generator; it is excluded from these results. This baseline is synthetic and represents a tiny, cache-friendly dataset. It does not predict throughput with many plans, more filters, or a remote deployment. Repeat the same command with a realistic dataset before tuning indexes or cache behavior.
