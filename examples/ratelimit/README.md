# Polaris Go

English | [中文](./README-zh.md)

## Service rate limiting

Polaris supports rate limiting on different request sources and system resources to protect services from being overwhelmed. This directory ships two end-to-end demos:

| Type | Resource (`Rule.Resource`) + action | Threshold | Key API | Sample provider |
| --- | --- | --- | --- | --- |
| **QPS limiting - reject** | `QPS` + `action=REJECT` | `Rule.Amounts[*].MaxAmount + ValidDuration` | `LimitAPI.GetQuota` (immediately rejects on excess) | `provider-qps` |
| **QPS limiting - unirate** | `QPS` + `action=UNIRATE` | `Rule.Amounts[*]` + `max_queue_delay` | `LimitAPI.GetQuota` (SDK queues the request and waits) | `provider-qps` (same binary, switched via `--service`) |
| **Concurrency limiting** | `CONCURRENCY` | `Rule.ConcurrencyAmount.MaxAmount` | `LimitAPI.GetQuota` + **`defer future.Release()`** | `provider-concurrency` |
| **Multi-dimensional match (AND)** | any resource + `Rule.Arguments[*]` | HEADER / QUERY / METHOD / CALLER_SERVICE / CALLER_IP / CALLER_METADATA | `quotaReq.AddArgument(model.BuildXxxArgument(...))` | `provider-qps` + consumer `--caller-*` injection |

> **reject vs unirate**: `reject` returns HTTP 429 immediately when the QPS threshold is exceeded; `unirate` instead **queues** excess requests inside the SDK (`QuotaFutureImpl.Get()` sleeps until the next quota tick) and only returns limited (HTTP 429) when the queueing delay exceeds `max_queue_delay`.
>
> **Multi-dimensional match**: rule `arguments` are joined with AND semantics; the rule fires only when all configured dimensions match. See `verify_ratelimit.sh` cases 4.x.
>
> Concurrency limiting is implemented by the `concurrency` plugin (`plugin/ratelimiter/reject_concurrency`).
> It is **node-local only**: even when the rule is pushed with `Type=GLOBAL`, the SDK forces it into local mode.

## Layout

```
examples/ratelimit/
├── consumer/                 Common consumer demo
├── provider-qps/             QPS limiting provider (Release is a no-op, kept for safety)
├── provider-concurrency/     Concurrency limiting provider (must defer Release)
├── verify_ratelimit.sh       End-to-end test script (QPS + concurrency)
├── cleanup.sh                Cleans up running providers and .build/.logs
├── test.md                   Test case numbering and pass criteria
└── README-zh.md / README.md
```

> To demonstrate "multiple instances of the same service", launch the same `provider-qps` binary twice
> with different `--port` flags, e.g. `./provider-qps/bin --port 18080` and `./provider-qps/bin --port 18081`.

## How to use

### Build executables

QPS demo:

```bash
cd provider-qps && go build -o bin && cd ..
cd consumer  && go build -o bin && cd ..
```

Concurrency demo:

```bash
cd provider-concurrency && go build -o bin && cd ..
```

### Create the service & rule

The QPS demo expects service `QpsRatelimitEchoServer`, the concurrency demo expects `ConcurrencyEchoServer` (both under namespace `default`).

Sample HTTP API calls:

```bash
# QPS rule: limit /echo to 2 RPS
curl -X POST http://127.0.0.1:8090/naming/v1/ratelimits \
  -H "X-Polaris-Token:${POLARIS_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '[{
    "name": "qps-rule",
    "service": "QpsRatelimitEchoServer",
    "namespace": "default",
    "resource": "QPS",
    "type": "LOCAL",
    "method": {"type": "EXACT", "value": "/echo"},
    "amounts": [{"maxAmount": 2, "validDuration": "1s"}],
    "action": "REJECT"
  }]'

# Concurrency rule: at most 2 in-flight requests for /slow
curl -X POST http://127.0.0.1:8090/naming/v1/ratelimits \
  -H "X-Polaris-Token:${POLARIS_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '[{
    "name": "concurrency-rule",
    "service": "ConcurrencyEchoServer",
    "namespace": "default",
    "resource": "CONCURRENCY",
    "type": "LOCAL",
    "method": {"type": "EXACT", "value": "/slow"},
    "concurrencyAmount": {"maxAmount": 2}
  }]'
```

> ⚠️ Concurrency rules must use `Resource=CONCURRENCY` and put the threshold in `concurrencyAmount.maxAmount`,
> not `amounts`. The framework selects the `concurrency` plugin automatically based on `Resource`,
> regardless of `Rule.Action`.

### Modify polaris.yaml

```yaml
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

### Run

```bash
# QPS
./provider-qps/bin --service QpsRatelimitEchoServer --port 18080 &
./consumer/bin --service QpsRatelimitEchoServer --port 18090 &

# Concurrency
./provider-concurrency/bin --service ConcurrencyEchoServer --port 18181 &
```

### Verify

#### QPS

```bash
curl -H 'user-id: polaris' http://127.0.0.1:18080/echo
# 1st & 2nd: Hello, I'm QpsRatelimitEchoServer Provider, ...
# 3rd onward: Too Many Requests
```

#### Concurrency

```bash
# Two concurrent slow calls keep the quota busy; the third is rejected immediately
curl http://127.0.0.1:18181/slow?ms=2000 &
curl http://127.0.0.1:18181/slow?ms=2000 &
sleep 0.2
curl http://127.0.0.1:18181/slow?ms=2000   # Too Many Requests
wait
# After the first two finish, quotas are returned automatically and new calls succeed
```

Key snippet from `provider-concurrency/main.go`:

```go
future, err := svr.limiter.GetQuota(quotaReq)
if future.Get().Code != model.QuotaResultOk {
    rw.WriteHeader(http.StatusTooManyRequests)
    return
}
// Concurrency limiting MUST release the quota when the request finishes,
// otherwise the in-flight counter leaks until everything is rejected.
defer future.Release()

doBusinessLogic()
```

## End-to-end test script

`verify_ratelimit.sh` runs the **full chain: `curl → consumer → provider`**. The consumer uses polaris service discovery to pick a provider instance and forwards the HTTP request; the provider invokes `LimitAPI.GetQuota` to apply the rate limit rule, and the resulting 429 is propagated back through the consumer to curl. This mirrors a real production call chain — different from the "curl directly to provider" quick-demo flow shown above.

```bash
cd examples/ratelimit
./verify_ratelimit.sh                          # Local polaris by default
./verify_ratelimit.sh --polaris-server 1.2.3.4
./verify_ratelimit.sh --skip qps,unirate       # Only run concurrency cases
./verify_ratelimit.sh --skip concurrency       # Only run QPS cases
./verify_ratelimit.sh --keep                   # Keep provider processes and logs for triage (rules are always kept)
```

Test case numbering and pass criteria: see [`test.md`](./test.md).

Cleanup:

```bash
./cleanup.sh -f          # Force cleanup of provider/consumer processes + .build/.logs
./cleanup.sh --dry-run   # Preview only
```
