# AIPool 局域网双机测试

目标：模型文件只由请求端用户选择和管理，朋友电脑只运行 AIPool Provider。首次推理时软件自动传输模型，后续复用内容缓存。

## 准备发布包

在开发电脑运行：

```powershell
.\scripts\install-provider-runtime.cmd
.\scripts\package-windows.cmd
```

将 `AIPool-Provider-Windows.zip` 发给朋友，将 `AIPool-Requester-Windows.zip` 放到用户电脑。Provider ZIP 已包含推理引擎，但不包含任何模型。

## 1. 朋友电脑：启动 Provider

解压后运行：

```powershell
.\start-provider-lan.cmd `
  -HostSecret '<host-secret>' `
  -ClientSecret '<client-secret>' `
  -LeaseSecret '<lease-secret>'
```

朋友不需要模型文件。Provider 启动 Control（8080）和 Host（8091）；托管 llama.cpp 只在收到模型并开始推理时启动，并只监听 `127.0.0.1:18081`。

如防火墙阻止局域网连接，以管理员身份仅对专用网络和本地子网放行：

```powershell
New-NetFirewallRule -DisplayName "AIPool LAN" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8080,8091 -Profile Private -RemoteAddress LocalSubnet
```

## 2. 用户电脑：选择本地模型并启动 Requester

扫描整个本地 GGUF 目录：

```powershell
.\start-requester-lan.cmd `
  -ControlURL 'http://192.168.1.20:8080' `
  -ClientSecret '<client-secret>' `
  -ModelDir 'D:\Models'
```

或者显式映射模型名：

```powershell
.\start-requester-lan.cmd `
  -ControlURL 'http://192.168.1.20:8080' `
  -ClientSecret '<client-secret>' `
  -Models 'qwen-local=D:\Models\qwen2.5.gguf'
```

Proxy 启动时计算本地模型 SHA-256。因此大模型第一次启动可能需要一些磁盘读取时间，但此时尚未上传。

## 3. 验证

```powershell
.\test-lan.cmd -Model 'qwen-local'
```

首次请求过程：

1. Proxy 用模型摘要、大小和显存要求申请租约。
2. Control 选择支持模型上传的在线 Provider。
3. Proxy 查询远端缓存偏移，并以 8 MiB 分块断点续传。
4. Provider 对完成文件计算 SHA-256，摘要一致才标记可用。
5. Provider 自动启动/切换托管 llama.cpp，加载缓存模型。
6. 推理结果以 JSON 或 SSE 返回用户电脑。

同一个模型再次推理时跳过上传。不同模型会按摘要分别缓存；当前单个 Provider 同一时刻串行执行托管模型推理，避免切换模型中断活跃请求。

## 排错

1. 请求端访问 `http://<朋友IP>:8080/healthz`。
2. 请求端访问 `http://<朋友IP>:8091/healthz`。
3. `GET http://127.0.0.1:11434/v1/models` 应只显示用户本地模型。
4. `selected host is unreachable` 表示 Host IP 或防火墙有问题。
5. `could not synchronize local model` 表示上传、租约过期、磁盘空间或摘要校验失败。
6. `managed runtime could not load model` 表示模型格式/硬件不兼容或托管 Runtime 启动失败。

## 当前限制

- 仅支持单文件 GGUF；暂不支持 safetensors 多文件模型。
- 首次使用必须上传完整模型，速度取决于局域网和磁盘。
- 远端缓存当前未加密，也没有容量淘汰和自动删除策略。
- 仅适用于可信私有局域网，不得映射到公网。
