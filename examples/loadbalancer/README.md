# Polaris Go

English | [中文](./README-zh.md)

## Using Load Balancing Feature

Experience the load balancing capability of Polaris quickly. This example provides a separate consumer directory for each load balancing algorithm.

## Directory Structure

```
loadbalancer/
├── provider/          # Common Provider
├── hash/              # Hash load balancing (hash)
├── l5cst/             # L5 consistent hash load balancing (l5cst)
├── maglev/            # Maglev hash load balancing (maglev)
├── ringhash/          # Ring hash load balancing (ringHash)
├── weightedRandom/    # Weighted random load balancing (weightedRandom)
├── verify_weight.sh   # Weight verification script
└── cleanup.sh         # Cleanup script
```

## How to use

### Build an executable

Build provider

```bash
cd ./provider
go build -o provider
```

Build a consumer for a specific algorithm (e.g., weightedRandom)

```bash
cd ./weightedRandom
go build -o consumer
```

### Accessing the Console

Create a corresponding service through the Polaris Console. If installed via the local one-click installation package, open the console directly in the browser at 127.0.0.1:8080

### Modifying Configuration

Specify the Polaris server address by editing the polaris.yaml file in each directory.

Each algorithm directory's polaris.yaml is pre-configured with the corresponding load balancing strategy, e.g., `hash/polaris.yaml`:

```yaml
consumer:
  loadbalancer:
    type: hash
```

### Execute program

Run the built **provider** executable

Run multiple providers on different nodes or specify different ports using the `--port` parameter.

```bash
./provider/provider
```

Run the built **consumer** executable (e.g., weightedRandom)

```bash
./weightedRandom/consumer
```

Each algorithm directory's consumer uses the corresponding load balancing strategy by default. You can also override it via command-line arguments:

```bash
# Hash-based algorithms require a hashKey
./hash/consumer --lbPolicy hash --hashKey "my-key"

# Weighted random algorithm
./weightedRandom/consumer --lbPolicy weightedRandom
```

### Supported Load Balancing Algorithms

| Directory | Algorithm | Description | Requires hashKey |
|-----------|-----------|-------------|-----------------|
| `hash/` | hash | Hash | ✅ Yes |
| `l5cst/` | l5cst | L5 Consistent Hash | ✅ Yes |
| `maglev/` | maglev | Maglev Hash | ✅ Yes |
| `ringhash/` | ringHash | Ring Hash | ✅ Yes |
| `weightedRandom/` | weightedRandom | Weighted Random | ❌ No |

### Using the Verification Script

Use `verify_weight.sh` to automatically verify load balancing behavior:

```bash
# Verify weightedRandom algorithm (default)
./verify_weight.sh

# Verify a specific algorithm
./verify_weight.sh --lb-policy hash
./verify_weight.sh --lb-policy ringHash
./verify_weight.sh --lb-policy maglev
```

### Verify

Requests will be load balanced to different providers.

```
curl http://127.0.0.1:17080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451

curl http://127.0.0.1:17080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.11:31102

curl http://127.0.0.1:17080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451
```