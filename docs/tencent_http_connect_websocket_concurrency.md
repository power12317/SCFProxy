# 腾讯云 `http-connect` WebSocket 并发模型与后续连接复用改造备忘

> 目的：记录当前 `http-connect` 方案的并发结论、腾讯云 WebSocket 函数模型，以及未来如果要改造成“本机多请求复用一个 WebSocket / 不同电脑独立实例”的连接池/多路复用方案，可以直接按本文拆分实现。

## 1. 当前结论

当前 `http-connect` 的实现是：

```text
客户端 / 浏览器 / curl
        ↓ HTTP CONNECT host:port
本地 SCFProxy http-connect 代理
        ↓ 每条 CONNECT 新建一条 WebSocket
腾讯云函数 URL / WSS 入口
        ↓ 云函数实例内 net.Dial("tcp", target)
目标 TCP 服务
```

关键结论：

1. **不是一个部署出来的 WebSocket 函数只能有一个连接。**
   - 一个“函数资源 / 函数 URL”可以被多个客户端同时连接。
   - 一个“函数实例 / 一次 WebSocket 调用”通常只服务一条 WebSocket 连接。

2. **腾讯云 WebSocket 函数的模型更接近：**

   ```text
   1 条 WebSocket 连接 ≈ 1 次函数调用 ≈ 1 个函数实例生命周期
   多条 WebSocket 连接 → 腾讯云调度多个函数实例
   ```

3. **当前代码不是本地只建一个总连接。**
   - 本地代理每接受一条新的 `CONNECT` 隧道，就调用一次 `dialTunnel(...)`。
   - `dialTunnel(...)` 每次都会发起一次新的 WebSocket 握手。
   - 所以当前是：

   ```text
   1 条 CONNECT 隧道 = 1 条 WebSocket = 1 个云函数实例连接
   ```

4. **多台电脑同时使用同一个腾讯函数 URL，正常不会因为“连接互斥”出错。**
   - 多台电脑会产生多条 WebSocket。
   - 腾讯云侧通过多个函数实例处理。
   - 真正限制来自腾讯云并发实例额度、函数超时、WebSocket 单连接/单实例限制、计费和网络吞吐。

## 2. 当前代码中的连接关系

当前本地代理入口在：

```text
httpconnect/proxy.go
```

核心逻辑类似：

```go
for {
    conn, err := ln.Accept()
    ...
    go handleClient(conn, opts.ApiUrl, opts.SecretKey)
}
```

每个客户端连接独立 goroutine 处理。

对于 `CONNECT` 请求，当前逻辑类似：

```go
tunnel, err := dialTunnel(apiURL, target, secretKey)
```

`dialTunnel(...)` 位于：

```text
httpconnect/websocket.go
```

它会：

1. 将函数 URL 从 `https://` 转成 `wss://`。
2. 将目标地址写到 query 参数：

   ```text
   ?target=example.com:443
   ```

3. 带上暗号请求头：

   ```text
   X-SCF-Secret-Key: <来自 sdk.toml 的 global.secret_key>
   ```

4. 发起 WebSocket Upgrade。
5. 返回一个 `net.Conn` 风格的 `wsConn`。

因此当前每条 CONNECT 都独立建立云端连接。

## 3. 多用户 / 多电脑行为说明

### 3.1 同一台电脑、同一个本地 SCFProxy 进程、多个系统账户或多个浏览器用户

如果多个用户实际都使用同一个本地代理地址，例如：

```text
127.0.0.1:9900
```

或局域网监听：

```text
0.0.0.0:9900
```

那么当前行为是：

```text
用户 A 的 CONNECT targetA → 本地 SCFProxy → WS 1 → 云函数实例 1
用户 B 的 CONNECT targetB → 本地 SCFProxy → WS 2 → 云函数实例 2
用户 C 的 CONNECT targetC → 本地 SCFProxy → WS 3 → 云函数实例 3
```

也就是说，当前没有“同一台电脑上多账户共享一条 WebSocket”的复用。

### 3.2 多台电脑各自运行 SCFProxy，指向同一个 `http_connect.json`

当前行为是：

```text
电脑 1 本地 SCFProxy → WS 1 → 云函数实例 1
电脑 2 本地 SCFProxy → WS 2 → 云函数实例 2
电脑 3 本地 SCFProxy → WS 3 → 云函数实例 3
```

每台电脑都有自己的本地进程和自己的 WebSocket 连接集合。

### 3.3 一台电脑作为局域网共享代理

如果一台电脑启动：

```bash
./SCFProxy http-connect -l 0.0.0.0:9900
```

其他电脑配置代理为：

```text
这台电脑的局域网 IP:9900
```

那么所有电脑请求都会进入这个本地 SCFProxy 进程。但在当前实现里，每条 CONNECT 仍然独立创建 WebSocket。

## 4. 腾讯云侧需要关注的限制

当前方案真正的限制不是“函数 URL 只能一个连接”，而是：

1. **并发实例额度**
   - 多条 WebSocket 连接会消耗多个函数实例。
   - 同时 CONNECT 数量越高，腾讯云函数并发实例数越高。

2. **函数最长执行时间**
   - 当前 `http-connect` 代码默认设置：

     ```go
     Timeout = 900
     ```

   - 即单条连接最长约 15 分钟，达到函数执行超时后会被平台断开。

3. **WebSocket 空闲超时**
   - 当前默认设置：

     ```go
     IdleTimeout = 600
     ```

   - 即连接空闲约 10 分钟后可能断开。

4. **单连接吞吐 / 包大小限制**
   - 腾讯云 WebSocket 函数有单连接包大小、请求速率、空闲时间等限制。
   - 当前代码也按 `256KB` 最大 frame payload 进行保护。

5. **计费模型**
   - `http` 事件函数模式更像短请求。
   - `http-connect` WebSocket 模式是长连接。
   - 连接不断，函数实例就持续运行并计费。

参考文档：

- 腾讯云 Web 函数概述：<https://cloud.tencent.com/document/product/583/56124>
- 腾讯云 WebSocket 协议支持：<https://cloud.tencent.com/document/product/583/63406>
- 腾讯云函数 URL：<https://cloud.tencent.com/document/product/583/96099>

## 5. 如果后续要改造成连接池 / 多路复用，目标形态

用户期望的后续目标形态：

```text
同一台电脑 / 同一个本地 SCFProxy 进程
    多个本地账户
    多个浏览器
    多个 CONNECT 请求
        ↓
    尽量复用同一条 WebSocket
        ↓
    腾讯云侧只占用一个 WebSocket 函数实例

不同电脑 / 不同本地 SCFProxy 进程
        ↓
    各自建立自己的 WebSocket
        ↓
    腾讯云侧各自对应不同函数实例
```

目标架构：

```text
电脑 A / 本地 SCFProxy 进程 A
    CONNECT 1 ┐
    CONNECT 2 ├─ 本地多路复用器 ─ 1 条 WS ─ 云函数实例 A ─ TCP target 1/2/3
    CONNECT 3 ┘

电脑 B / 本地 SCFProxy 进程 B
    CONNECT 4 ┐
    CONNECT 5 ├─ 本地多路复用器 ─ 1 条 WS ─ 云函数实例 B ─ TCP target 4/5
```

也就是说：

```text
复用粒度 = 本地进程级别
默认每个本地 SCFProxy 进程维护 1 条主 WebSocket
同一进程内不同账户/不同客户端/不同 CONNECT 共享这条主 WebSocket
不同电脑因为是不同进程，所以自然独立 WebSocket / 独立云函数实例
```

## 6. 多路复用协议草案

后续如果要实现复用，不应该继续使用当前 query 参数 `target=host:port` 代表单目标连接。

新的 WebSocket 应该变成“控制通道 + 数据通道”，在同一条 WS 上承载多条逻辑 TCP stream。

### 6.1 逻辑 stream 概念

每个本地 CONNECT 分配一个 `stream_id`：

```text
CONNECT example.com:443 → stream_id = 1
CONNECT api.example.com:443 → stream_id = 2
CONNECT 1.2.3.4:22 → stream_id = 3
```

同一条 WebSocket 上传输带 `stream_id` 的 frame。

### 6.2 建议 frame 类型

可以定义一个简单二进制协议。

通用头：

```text
0               1               2               3
+---------------+---------------+---------------+---------------+
| version(1)    | type(1)       | flags(1)      | reserved(1)   |
+---------------+---------------+---------------+---------------+
| stream_id uint32 big endian                                   |
+---------------------------------------------------------------+
| payload_len uint32 big endian                                 |
+---------------------------------------------------------------+
| payload ...                                                   |
+---------------------------------------------------------------+
```

建议类型：

| type | 名称 | 方向 | payload |
|---:|---|---|---|
| `0x01` | `OPEN` | local → cloud | JSON：`{"target":"host:port"}` |
| `0x02` | `OPEN_OK` | cloud → local | 可为空 |
| `0x03` | `OPEN_FAIL` | cloud → local | 错误消息 |
| `0x04` | `DATA` | 双向 | TCP bytes |
| `0x05` | `CLOSE` | 双向 | 可为空，表示半关闭或关闭 |
| `0x06` | `RESET` | 双向 | 错误消息，强制关闭 stream |
| `0x07` | `PING` | 双向 | 可为空 |
| `0x08` | `PONG` | 双向 | 可为空 |
| `0x09` | `WINDOW_UPDATE` | 双向 | 可选，用于流控 |

### 6.3 OPEN 流程

本地收到 CONNECT：

```text
客户端 → 本地代理：CONNECT example.com:443
```

本地代理不再立即创建新的 WebSocket，而是：

1. 从本地 mux session 取一个新的 `stream_id`。
2. 通过已有 WebSocket 发送：

   ```text
   OPEN stream_id=123 payload={"target":"example.com:443"}
   ```

3. 云函数收到 `OPEN` 后：

   ```go
   net.DialTimeout("tcp", target, 10*time.Second)
   ```

4. 成功则返回：

   ```text
   OPEN_OK stream_id=123
   ```

5. 本地收到 `OPEN_OK` 后，才给客户端返回：

   ```http
   HTTP/1.1 200 Connection Established


   ```

6. 后续客户端 TCP bytes 都以：

   ```text
   DATA stream_id=123 payload=<bytes>
   ```

   发送给云端。

### 6.4 CLOSE / RESET 流程

任一侧连接关闭时：

```text
CLOSE stream_id=123
```

如果发生错误：

```text
RESET stream_id=123 payload="dial tcp failed: ..."
```

需要注意 TCP 半关闭语义：

- HTTP CONNECT 后的 TLS 连接有时需要支持单向关闭。
- Go 的 `net.Conn` 普通 `Close()` 是双向关闭。
- 如果要更精确，可以对 `*net.TCPConn` 使用 `CloseRead()` / `CloseWrite()`。
- 第一版可以先简单处理成双向关闭，满足浏览器和 curl 绝大多数场景。

## 7. 本地侧改造建议

新增核心组件：

```text
httpconnect/mux_client.go
```

建议结构：

```go
type MuxClient struct {
    apiURL    string
    secretKey string

    mu        sync.Mutex
    ws        net.Conn
    nextID    uint32
    streams   map[uint32]*MuxStream

    closed    chan struct{}
}

type MuxStream struct {
    id       uint32
    readCh   chan []byte
    errCh    chan error
    closeCh  chan struct{}
}
```

### 7.1 本地连接池策略

最符合用户当前需求的默认策略：

```text
每个 provider.region / 每个函数 URL / 每个本地 SCFProxy 进程：维护 1 条 WebSocket mux session
```

也就是说：

```text
http_connect.json 里 1 条腾讯记录 → 本地启动 1 个监听端口 → 这个监听端口内部维护 1 条 WSS 主连接
```

如果一个进程启动多个地域记录，则每个地域一条主连接：

```text
ap-guangzhou 监听 9900 → 1 条 WSS 到广州函数
ap-shanghai  监听 9901 → 1 条 WSS 到上海函数
```

### 7.2 本地请求处理变化

当前：

```go
handleConnect(...) {
    tunnel, err := dialTunnel(apiURL, target, secretKey)
    io.Copy(...)
}
```

未来改成：

```go
handleConnect(...) {
    stream, err := muxClient.Open(target)
    io.Copy(... stream ...)
}
```

也就是本地 `CONNECT` 仍然暴露为普通 `net.Conn` 风格，但底层不再是一条 WebSocket，而是一个 mux stream。

### 7.3 主 WebSocket 断线处理

需要定义策略：

1. 主 WS 断开时，所有活跃 stream 都应该 `RESET`。
2. 后续新 CONNECT 触发自动重连。
3. 可选：后台 heartbeat，发现断线主动重连。
4. 不建议透明迁移已有 TCP stream，因为远端 TCP 连接也在旧云函数实例里，WS 断了就无法恢复。

### 7.4 是否允许配置池大小

虽然用户目标是“同一台电脑复用一条 WebSocket”，但从性能角度建议保留扩展点：

```text
pool_size = 1 默认
```

后续如遇腾讯单连接吞吐瓶颈，可以扩展为：

```text
pool_size = 2/4/8
按 stream_id 或 round-robin 分配到不同主 WS
```

但第一版不要加 CLI 参数，避免复杂化。可以先代码里常量：

```go
const defaultMuxPoolSize = 1
```

## 8. 云函数侧改造建议

当前云函数代码在：

```text
function/httpconnect/tencent/main.go
```

当前模型：

```text
一个 WS 请求带 target query
云函数直接 dial 一个 target
WS <-> TCP 一对一转发
```

未来模型：

```text
一个 WS 请求不再绑定单个 target
云函数启动 mux server
每个 OPEN frame 创建一个远端 TCP 连接
多个 stream 并发共用同一条 WS
```

建议新增云端结构：

```go
type MuxServer struct {
    ws      net.Conn
    mu      sync.Mutex // 写 WS frame 必须串行
    streams map[uint32]*RemoteStream
}

type RemoteStream struct {
    id   uint32
    conn net.Conn
}
```

云端行为：

1. WebSocket Upgrade 成功后，创建 `MuxServer`。
2. 循环读取 WebSocket binary frame。
3. 解出 mux frame。
4. 根据 `type` 分派：
   - `OPEN`：dial target，创建 stream。
   - `DATA`：写入对应 TCP conn。
   - `CLOSE`：关闭对应 TCP conn。
   - `RESET`：强制关闭对应 TCP conn。
5. 每个远端 TCP conn 启动 goroutine 读取数据，读到后写回：

   ```text
   DATA stream_id=<id> payload=<tcp bytes>
   ```

注意：多个远端 TCP goroutine 会并发写同一条 WebSocket，所以必须使用统一写锁。

## 9. 复用方案的优点和风险

### 9.1 优点

1. **同一台电脑上不同账户/不同浏览器可以共用一个云函数实例。**
2. **显著降低腾讯云并发实例数。**
3. **减少 WebSocket 握手和云函数冷启动次数。**
4. **更接近“一个本地代理进程 = 一个云端会话”的模型。**

### 9.2 风险 / 缺点

1. **单 WebSocket 连接成为瓶颈。**
   - 多个 TCP stream 共享一条 WS。
   - 如果腾讯云单连接存在 128KB/s 等限制，所有复用流会一起被限制。

2. **故障影响面扩大。**
   - 当前一条 CONNECT 断只影响一个目标连接。
   - 复用后主 WS 断开，会导致本机所有通过该 WS 的 CONNECT 全部断开。

3. **实现复杂度明显增加。**
   - 需要 stream_id、frame parser、关闭语义、错误传播、背压、流控。

4. **函数最大执行时间仍然存在。**
   - 即使复用成一条 WS，腾讯云函数实例运行到最大超时时间后仍会断。
   - 所有复用 stream 会一起断。

5. **不适合大流量下载/视频/测速。**
   - 复用会降低并发实例数量，但可能牺牲吞吐。

## 10. 推荐改造顺序

如果后续决定实现复用，建议按下面顺序做，不要一次性大改：

### 阶段 1：抽象当前一对一隧道接口

目标：不改行为，只改结构。

新增接口：

```go
type TunnelDialer interface {
    Open(target string) (net.Conn, error)
}
```

当前 `dialTunnel(...)` 包装成：

```go
type DirectWSDialer struct { ... }
```

这样当前代码仍然是一条 CONNECT 一条 WS。

### 阶段 2：实现 mux frame 编解码单元测试

新增：

```text
httpconnect/mux_frame.go
httpconnect/mux_frame_test.go
```

只测试：

- encode/decode 正常。
- 大小端一致。
- payload 长度校验。
- 未知 type 处理。
- 超大 payload 拒绝。

### 阶段 3：本地实现 `MuxClient`

新增：

```text
httpconnect/mux_client.go
```

先不接入 CLI，用单元测试 + 本地 fake server 测试：

- `Open(target)` 分配 stream。
- `DATA` 正确路由到对应 stream。
- `CLOSE/RESET` 正确关闭单个 stream。
- 主 WS 断开时所有 stream 返回错误。

### 阶段 4：云函数实现 mux server

改造：

```text
function/httpconnect/tencent/main.go
```

从一对一模式改成：

```text
一条 WS 内多个 stream
```

可以保留兼容：

- 如果 URL 有 `target` query，则走旧一对一逻辑。
- 如果没有 `target`，则走新 mux 逻辑。

这样方便灰度和回滚。

### 阶段 5：本地切换默认 dialer

将本地 `http-connect` 默认从 `DirectWSDialer` 切换到 `MuxClient`。

默认策略：

```text
每个本地监听端口 / 每条 http_connect.json 记录 = 1 个 MuxClient = 1 条主 WS
```

如果后续需要保留旧模式，可以做隐藏常量或配置项，不一定加公开 CLI 参数。

### 阶段 6：压测和费用观察

测试用例：

```bash
# 单连接
curl -x http://127.0.0.1:9900 https://ifconfig.me

# 多目标并发
seq 1 20 | xargs -I{} -P20 curl -x http://127.0.0.1:9900 -s https://example.com -o /dev/null

# 同一目标多连接
seq 1 20 | xargs -I{} -P20 curl -x http://127.0.0.1:9900 -s https://ifconfig.me
```

重点观察：

- 本地是否只建立 1 条主 WSS。
- 腾讯云函数实例数量是否下降。
- 单连接吞吐是否成为瓶颈。
- 主 WS 断开时错误是否可控。
- 900 秒超时是否导致所有复用流同时断开。

## 11. 未来可能需要的日志

为了调试连接复用，建议本地日志增加：

```text
mux session connected provider=tencent region=ap-guangzhou
stream open id=123 target=example.com:443
stream close id=123 duration=10s bytes_in=... bytes_out=...
mux session closed active_streams=...
```

云函数侧如果关闭 CLS，则这些日志看不到；但本地侧日志足够判断大部分问题。

注意不要打印：

- `sdk.toml` 内容
- SecretId / SecretKey
- `global.secret_key`
- `X-SCF-Secret-Key`

## 12. 当前建议

当前先保持“一条 CONNECT 一条 WebSocket”的实现，原因：

1. 语义简单，稳定性更好。
2. 每条连接故障隔离。
3. 避免腾讯云单 WS 吞吐限制影响所有请求。
4. 方便先验证腾讯 Web Function + Function URL + WebSocket 这条链路是否可用。

等确认以下问题后，再考虑复用：

1. 腾讯云 WebSocket Web Function 部署参数在真实环境稳定。
2. 普通 HTTPS CONNECT 能稳定跑通。
3. 费用或并发实例数确实成为问题。
4. 单连接吞吐限制对使用场景影响可接受。

如果后续明确目标是“同一台电脑所有账户共用一个云函数实例，不同电脑独立实例”，则按本文第 5~10 节实现 `MuxClient + MuxServer + stream_id` 多路复用即可。
