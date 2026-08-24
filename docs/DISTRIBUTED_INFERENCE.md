# AIPool 分布式单次推理架构

此模块与现有单节点推理隔离。当前目标是先固定组队、分层和热路径协议，再将真实 GGUF/llama.cpp 分层后端接入同一接口，避免破坏已经可用的单节点和多任务并行模式。

## 当前已经实现

- `POST /v1/distributed/groups` 原子预留至少两台分布式节点。
- 按显存/内存容量为每台节点分配连续 Transformer 层区间。
- 每个 Stage 租约绑定 Group ID、Stage Index、节点、模型和层区间。
- `POST /v1/distributed/groups/{groupID}/release` 原子释放整组容量。
- `tensorwire` 固定二进制激活帧：会话 ID、序列号、Token 位置、张量宽度、HMAC。
- Stage 会话拒绝乱序数据和被篡改的张量帧。
- TCP Stage 客户端复用持久连接，不为每个 Token 重新握手。
- 三 Stage 连续层流水线测试与单进程执行同样层范围逐值一致。
- 独立 `stage.exe`，当前使用确定性数学后端作为协议正确性工具。
- 真实 GGUF v2/v3 元数据解析：架构、层数、隐藏维度和逐层张量字节数。
- 基于真实逐层张量大小的连续层容量规划，不拆分单层。
- 容量规划把 embedding/output 等非 Transformer block 张量计入首尾 Stage 的预算。
- llama.cpp RPC 真实执行适配：用户端运行协调 `llama-server`，至少两台 Provider 运行 `rpc-server`。
- 每个 RPC Worker 只监听 Provider 本机地址，通过 AIPool 端到端加密反向隧道映射到请求端。
- Proxy 的 `X-AIPool-Execution: distributed` 路径会组队、保持组租约、复用模型常驻的 llama.cpp 会话并转发 OpenAI 请求。

确定性 Stage 后端不是语言模型，也不会生成自然语言。它仍作为自研 Stage 协议的正确性工具保留。当前第一版真实模型执行采用固定版本 llama.cpp RPC，以复用成熟 GGUF、CPU/CUDA 算子和 KV Cache；AIPool 负责组调度、身份、加密隧道和生命周期。

## 模块边界

```text
OpenAI Proxy
    │
    ├── 现有单节点路径 ── Host ── llama.cpp
    │
    └── 分布式路径 ── Group Scheduler
                         │
                         └── Distributed Session
                               Stage 0 → Stage 1 → Stage 2
```

- `internal/distributed`：组计划和会话编排。
- `internal/stage`：独立连续层执行边界和持久 TCP 客户端。
- `internal/tensorwire`：热路径二进制协议。
- `cmd/stage`：实验 Stage 进程。

## 当前真实执行边界

- 当前真实模型执行采用 llama.cpp RPC；Provider 不保存完整 GGUF 文件，但执行期间内存中必然出现分配到该节点的权重张量。
- AIPool 的层范围是调度和授权计划；当前 `rpc-server` 不理解 AIPool Stage Token，因此不会在 Worker 内强制核验 `LayerStart/LayerEnd`。实际张量分布由 `--split-mode layer` 和 `--tensor-split` 控制。
- 第一版公网热路径经过自建 Relay 的 TLS + AES-256-GCM 隧道，优先验证正确性；高延迟网络的性能优化需要后续 P2P QUIC、连接复用、预热和激活压缩。
- 自研 `stage`/`tensorwire` 仍作为未来可精确控制连续层、激活格式和流水线的实验路径，不代表当前真实 GGUF 执行后端。

## 本机真实模型门禁测试

完整 Runtime 下载完成后，开发机使用两个独立 `rpc-server` 进程执行同一个真实 GGUF：

```powershell
.\scripts\smoke-distributed-real.cmd
.\scripts\smoke-distributed-pool-real.cmd
.\scripts\smoke-distributed-relay-real.cmd
```

第一个脚本验证 llama.cpp 原生双 Worker；第二个脚本验证 AIPool Control、两个 Host、分布式组调度、Proxy、协调 llama-server 和 OpenAI 请求的完整链路；第三个脚本再把远端 RPC 热路径穿过自建 TLS Relay 与 AES-256-GCM 隧道。三者通过后才进入两台物理电脑人工测试。

在真实后端完成前，分布式 API 与 `stage.exe` 属于开发者实验接口，不应作为真实模型推理能力对外宣称。
