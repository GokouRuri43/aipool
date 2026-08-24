# AIPool

<p align="center">
  <strong>A personal AI compute pool under your control</strong><br>
  Let your own computer and authorized friends' computers work together on local LLM inference.
</p>

<p align="center">
  <a href="README.md"><img alt="简体中文 README" src="https://img.shields.io/badge/README-%E7%AE%80%E4%BD%93%E4%B8%AD%E6%96%87-d73a49"></a>
</p>

> [!IMPORTANT]
> AIPool is currently an early prototype for development and testing. It is not a production-ready compute platform. Real multi-machine testing across the public Internet, heterogeneous hardware validation, and failure recovery work are still required.

## What is AIPool?

AIPool is designed around a simple ownership model: the requester keeps and manages model files on their own computer, while a friend only installs and runs AIPool Provider. The friend does not need to find a model, configure llama.cpp, or operate CUDA tools. AI applications connect to an OpenAI-compatible API on the requester's machine. AIPool can route each request to one node or split one inference run across multiple nodes.

```text
AI application
   │  OpenAI-compatible API (localhost only)
   ▼
Requester: Proxy + Control + local GGUF
   │
   ├── Single-node inference ─────────► one Provider (local or remote)
   │
   └── Distributed inference ─────────► local + friend 1 + friend 2 ...
                 llama.cpp RPC            weight tensors allocated by RAM/VRAM
```

Machines can connect directly on a trusted LAN. Across different networks, both sides establish outbound connections to an AIPool Relay that you operate yourself. This path does not depend on Tailscale, Headscale, or another company's hosted service.

## Where is the model?

- The requester chooses, names, and manages the source model and model directory locally.
- In single-node mode, the complete GGUF is transferred in 8 MiB chunks on first use. The Provider verifies its SHA-256 digest and stores it in an AIPool-managed cache for later requests.
- In distributed mode, a coordinator `llama-server` on the requester reads the local GGUF and uses llama.cpp RPC to place assigned weight tensors in each worker's memory. A friend does not need to keep or manually manage the complete GGUF file.
- Remote computation cannot make the participating machine completely unaware of the data it processes. The single-node cache is currently stored in plaintext, so only trusted and explicitly authorized machines should be used.

## Current capabilities

### Personal multi-node pool

- One requester can register its own Provider and multiple friends' Providers.
- Independent concurrent requests can be distributed by load, concurrency capacity, and free VRAM.
- A request can select a node or restrict execution to local or remote Providers. Response headers report the selected node.
- Providers automatically detect NVIDIA GPUs, VRAM, CPUs, and system memory and report their state to Control.

### One inference run across multiple computers

- At least two nodes are reserved atomically for one inference run. The complete reservation fails if any required capacity is unavailable.
- GGUF v2/v3 metadata is parsed to plan contiguous layer ranges from actual per-layer tensor sizes.
- The current real execution backend uses llama.cpp RPC with `--split-mode layer` and `--tensor-split`.
- RPC workers listen only on each Provider's loopback address and are mapped to the requester through encrypted AIPool tunnels.
- Distributed sessions support group lease renewal, persistent connections, and reuse of resident models.
- The experimental Stage/TensorWire path implements ordered HMAC-protected activation frames and a contiguous-stage pipeline, but it is a protocol validation backend rather than the production GGUF backend.

### API, model transfer, and runtime

- Local OpenAI-compatible `GET /v1/models` and `POST /v1/chat/completions` endpoints.
- Regular JSON and SSE streaming responses.
- Local GGUF directory discovery and explicit `model-id=local-path` mappings.
- SHA-256 content addressing, resumable model upload, and integrity verification.
- Automatic lifecycle management for the bundled llama.cpp runtime on Providers.
- Separate secrets for Host registration, Requester access, and lease signing.

### Connectivity across different networks

- Self-hosted Relay with TLS 1.3 and SHA-256 certificate fingerprint pinning.
- AES-256-GCM end-to-end encryption derived from the pairing key.
- Random challenges and Ed25519 signatures for Relay authentication; the Pair Token is never sent to the Relay.
- Both peers use outbound connections, so they do not need to share a LAN, own a public IP, or configure router port forwarding.
- The Relay sees connection metadata and ciphertext sizes but cannot read models, prompts, responses, or internal AIPool secrets.

## Development status

| Component | Status | Current boundary |
|---|---|---|
| Control, Host, Proxy, and OpenAI-compatible API | Implemented and covered by automated tests | Wider API compatibility and a stable error contract remain |
| Single-Provider mock inference | Verified | Checks the end-to-end path but does not represent real-model performance |
| Single-Provider GGUF inference | Implemented | Physical two-machine and heterogeneous CPU/GPU testing is pending |
| Multi-Provider registration and concurrent scheduling | Implemented with unit/integration coverage | Real load, disconnect, and recovery testing is pending |
| One inference run split across nodes | llama.cpp RPC path integrated | Local smoke scripts are ready; remote hardware performance and stability remain unverified |
| Experimental Stage/TensorWire | Protocol and deterministic pipeline implemented | Full Transformer/GGUF operators are not connected |
| Self-hosted Internet Relay | Encrypted transport and authentication implemented with E2E tests | Public NAT, long-running, and poor-network tests are pending |
| Windows Provider/Requester packages | Runtime download, packaging, and verification scripts implemented | No GUI installer, updater, or code signing yet |
| Production accounts, authorization, quotas, and operations | Not implemented | Pair files are powerful credentials intended only for trusted testing |

The code prototype and automated validation are in place. The next milestone requires at least two physical computers and one self-hosted public Relay to test real GGUF models, heterogeneous hardware, cross-NAT operation, reconnection, throughput, and time to first token.

## Quick start: model-free mock

The development environment requires Windows PowerShell and Go 1.26 or later:

```powershell
git clone https://github.com/GokouRuri43/aipool.git
cd aipool
.\scripts\build.cmd
.\scripts\demo.cmd
```

This starts Control, a mock Host, and Proxy locally, sends one OpenAI-compatible request, and then stops the processes. It neither downloads nor runs a model.

## Build Windows test packages

Download a llama.cpp runtime once, then create separate Provider and Requester archives:

```powershell
.\scripts\install-provider-runtime.cmd -Variant CUDA
.\scripts\package-windows.cmd
```

Use `-Variant CPU` when no NVIDIA GPU is available. Outputs:

- `dist/AIPool-Provider-Windows.zip`: for a friend; includes Host, Tunnel, Stage, and the managed runtime, but no model.
- `dist/AIPool-Requester-Windows.zip`: for the requester; includes Control, Proxy, Tunnel, Host, and test tools.

The friend does not run the runtime installer. They extract the complete Provider archive and use its startup script.

## Minimal LAN test

On the friend's computer:

```powershell
.\start-provider-lan.cmd `
  -HostSecret '<host-secret>' `
  -ClientSecret '<client-secret>' `
  -LeaseSecret '<lease-secret>'
```

On the requester's computer:

```powershell
.\start-requester-lan.cmd `
  -ControlURL 'http://FRIEND_LAN_IP:8080' `
  -ClientSecret '<client-secret>' `
  -Models 'qwen-local=D:\Models\model.gguf'
```

Then run:

```powershell
.\test-lan.cmd -Model 'qwen-local'
```

See the [LAN testing guide](docs/LAN_TESTING.md) for complete steps, firewall rules, and troubleshooting. LAN ports must only be exposed to a private local subnet, never forwarded to the Internet.

## Minimal cross-network test

First deploy `relay.exe` on a public server you control, allow inbound TCP 8443, and generate or configure a TLS certificate. On the requester:

```powershell
.\start-requester-internet.cmd `
  -RelayAddress 'relay.example.com:8443' `
  -RelayServerName 'relay.example.com' `
  -RelayFingerprint '<SHA-256 fingerprint>' `
  -ProviderName 'friend-1' `
  -Mock
```

The script creates a Provider-specific `AIPool-Pair-friend-1.json` on the desktop. Send it to that friend through a trusted channel. The friend runs:

```powershell
.\start-provider-internet.cmd -PairFile '.\AIPool-Pair-friend-1.json' -Mock
```

In a second terminal on the requester:

```powershell
.\test-internet.cmd -Model 'mock-llm'
```

After the mock succeeds, remove `-Mock` on both sides and select a local GGUF with `-ModelDir` or `-Models`. Add another friend with `-ProviderName 'friend-2' -AddProvider`. To enable one inference run across two nodes, add:

```powershell
-Distributed -LocalProvider -DistributedMinNodes 2 -DistributedMaxNodes 2
```

If two friends' Providers are available, `-LocalProvider` can be omitted. See the [cross-network testing guide](docs/INTERNET_TESTING.md) for Relay deployment, pairing, and the complete test procedure.

## Client API

Configure an AI application to use the local endpoint:

```text
http://127.0.0.1:11434/v1
```

Optional request headers:

| Header | Purpose |
|---|---|
| `X-AIPool-Node-ID: friend-1` | Select an execution node |
| `X-AIPool-Scope: local` | Allow only the local Provider |
| `X-AIPool-Scope: remote` | Allow only friends' Providers |
| `X-AIPool-Min-VRAM-MB: 4096` | Require a minimum amount of free VRAM |
| `X-AIPool-Execution: distributed` | Request one distributed inference run |

`X-AIPool-Node-ID` identifies a single-node executor. Distributed responses include `X-AIPool-Execution: distributed` and `X-AIPool-Nodes`.

## Security boundaries

- Connect only devices that you trust and whose owners have explicitly authorized Provider use.
- Pair files contain powerful capability credentials and end-to-end encryption keys. Protect them like passwords and transfer them only through a trusted channel.
- Accounts, friend-side approval UI, device revocation, usage quotas, sandboxing, cache encryption, and automatic cache cleanup are not implemented yet.
- A Provider processes model weights and user requests. AIPool cannot preserve confidentiality against a compromised Provider host.
- Relay payloads are end-to-end encrypted, but the Relay can still observe metadata such as Pair ID, connection time, target category, and byte counts.
- The current public-network hot path goes through the Relay to prioritize connectivity and protocol correctness. P2P QUIC, activation compression, and production-grade poor-network optimization are not implemented.

## Known limitations and roadmap

- Only single-file GGUF models are supported; safetensors, multi-file weights, and training jobs are not supported.
- Add regression testing on real two/multi-machine setups, mixed GPUs, CPU-only workers, and public networks.
- Optimize initial weight transfer, connection reuse, warmup, KV cache, and model-group lifecycle.
- Add direct P2P QUIC with fallback to the self-hosted Relay.
- Add friend-side approval, resource caps, thermal/power policies, pause, and one-click revocation.
- Add accounts/device identities, short-lived credentials, revocation, quotas, auditing, and abuse protection.
- Add encrypted cache storage, eviction, cleanup, desktop UI, installers, updates, and code signing.

See [Distributed inference architecture](docs/DISTRIBUTED_INFERENCE.md) for implementation details and exact boundaries.

## Development verification

```powershell
go test ./...
go vet ./...
.\scripts\smoke-relay.cmd
```

Local real-llama.cpp gate scripts:

```powershell
.\scripts\smoke-distributed-real.cmd
.\scripts\smoke-distributed-pool-real.cmd
.\scripts\smoke-distributed-relay-real.cmd
```

These smoke tests do not replace manual tests on multiple physical computers.
