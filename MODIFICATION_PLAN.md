# HTTP代理多端口模式改造文档

## 需求

1. **多端口模式**: 每个云函数对应一个本地端口，启动时自动开启所有端口
2. **调整函数规格**: Timeout 120秒, CPU 0.05核, Memory 128MB, Disk 512MB
3. **去proxy化**: 移除namespace和函数名中的"proxy"关键字

## 核心逻辑

- **部署**: `scfproxy deploy http -p alibaba,tencent -r cn-hangzhou,cn-shenzhen` → 创建多个云函数，名称均为 `scf_http`
- **启动**: `scfproxy http -l :9900` → 自动开启 9900, 9901, 9902... 对应每个云函数URL

---

## 涉及文件清单

### 1. cmd/provider.go
### 2. cmd/config/http.go
### 3. cmd/http.go
### 4. http/proxy.go
### 5. http/modifier.go
### 6. sdk/provider/alibaba/http.go
### 7. sdk/provider/tencent/http.go
### 8. sdk/provider/aws/http.go

---

## 详细修改

### 1. cmd/provider.go

**修改常量定义**

```go
// 修改前
const (
    Namespace = "scfproxy"
    HTTPFunctionName = "scf_http"
    HTTPTriggerName  = "http_trigger"
)

// 修改后
const (
    Namespace = "scf"
    HTTPFunctionName = "scf_http"
    HTTPTriggerName  = "http_trigger"
)
```

---

### 2. cmd/config/http.go

**修改HttpRecord结构，添加GetAllUrls方法**

```go
// 修改前
type HttpRecord struct {
    Api string
}

// 修改后
type HttpRecord struct {
    Port int    // 保留字段，用于兼容
    Url  string
}

// 新增方法：获取所有URL
func (c *HttpConfig) GetAllUrls() []string {
    c.mu.RLock()
    defer c.mu.RUnlock()
    var urls []string
    for _, rmap := range c.Records {
        for _, record := range rmap {
            if record.Url != "" {
                urls = append(urls, record.Url)
            }
        }
    }
    return urls
}
```

**修改ToDoubleArray方法**

```go
// 修改前
func (c *HttpConfig) ToDoubleArray() [][]string {
    data := [][]string{}
    for provider, rmap := range c.Records {
        for region, record := range rmap {
            data = append(data, []string{provider, region, record.Api})
        }
    }
    return data
}

// 修改后
func (c *HttpConfig) ToDoubleArray() [][]string {
    data := [][]string{}
    for provider, rmap := range c.Records {
        for region, record := range rmap {
            data = append(data, []string{provider, region, record.Url})
        }
    }
    return data
}
```

**修改AvailableApis方法**

```go
// 修改前
func (c *HttpConfig) AvailableApis() []string {
    var apis []string
    for _, rmap := range c.Records {
        for _, record := range rmap {
            r, ok := interface{}(record).(*HttpRecord)
            if !ok {
                return apis
            }
            if r.Api != "" {
                apis = append(apis, r.Api)
            }
        }
    }
    return apis
}

// 修改后
func (c *HttpConfig) AvailableApis() []string {
    return c.GetAllUrls()
}
```

---

### 3. cmd/http.go

**完全重写启动逻辑**

```go
package cmd

import (
    "errors"
    "fmt"
    "net"
    "strconv"

    "github.com/sirupsen/logrus"
    "github.com/spf13/cobra"

    "github.com/shimmeris/SCFProxy/cmd/config"
    "github.com/shimmeris/SCFProxy/http"
)

var (
    listenAddr string
    certPath   string
    keyPath    string
)

var httpCmd = &cobra.Command{
    Use:   "http",
    Short: "Start http proxy",
    RunE: func(cmd *cobra.Command, args []string) error {
        conf, err := config.LoadHttpConfig()
        if err != nil {
            return err
        }

        // 获取所有已部署的URL
        urls := conf.GetAllUrls()
        if len(urls) == 0 {
            return errors.New("no deployed functions found")
        }

        // 解析基础端口
        host, portStr, err := net.SplitHostPort(listenAddr)
        if err != nil {
            return err
        }
        basePort, _ := strconv.Atoi(portStr)

        // 启动所有端口
        for i, url := range urls {
            port := basePort + i
            go func(port int, apiUrl string) {
                opts := &http.Options{
                    ListenAddr: fmt.Sprintf("%s:%d", host, port),
                    CertPath:   certPath,
                    KeyPath:    keyPath,
                    ApiUrl:     apiUrl,
                }
                if err := http.ServeProxy(opts); err != nil {
                    logrus.Errorf("Port %d failed: %v", port, err)
                }
            }(port, url)
        }

        logrus.Infof("Started %d HTTP proxies on ports %d-%d", len(urls), basePort, basePort+len(urls)-1)

        // 阻塞主线程
        select {}
    },
}

func init() {
    rootCmd.AddCommand(httpCmd)
    httpCmd.Flags().StringVarP(&listenAddr, "listen", "l", "", "host:port of the proxy (base port)")
    httpCmd.Flags().StringVarP(&certPath, "certPath", "c", config.CertPath, "filepath to the CA certificate")
    httpCmd.Flags().StringVarP(&keyPath, "keyPath", "k", config.KeyPath, "filepath to the private key")

    httpCmd.MarkFlagRequired("listen")
}
```

---

### 4. http/proxy.go

**修改Options结构**

```go
// 修改前
type Options struct {
    ListenAddr string
    CertPath   string
    KeyPath    string
    Apis       []string
}

// 修改后
type Options struct {
    ListenAddr string
    CertPath   string
    KeyPath    string
    ApiUrl     string
}
```

**修改ServeProxy函数**

```go
func ServeProxy(opts *Options) error {
    if opts.ApiUrl == "" {
        return errors.New("api URL is required")
    }

    p := martian.NewProxy()
    defer p.Close()

    // Prevent scfproxy from recursively connecting to itself.
    _, lport, _ := net.SplitHostPort(opts.ListenAddr)
    p.SetDial(func(network, address string) (net.Conn, error) {
        host, port, _ := net.SplitHostPort(address)
        if port == lport && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
            return nil, errors.New("Detecting recursive connection")
        }
        return net.Dial(network, address)
    })

    l, err := net.Listen("tcp", opts.ListenAddr)
    if err != nil {
        logrus.Fatal(err)
    }

    if err := configureTls(p, opts.CertPath, opts.KeyPath); err != nil {
        logrus.Error(err)
    }

    modifier, err := NewScfModifier(opts.ApiUrl, lport)
    if err != nil {
        return err
    }

    p.SetRequestModifier(modifier)
    p.SetResponseModifier(modifier)

    go func() {
        c := make(chan os.Signal, 1)
        signal.Notify(c, os.Interrupt, syscall.SIGTERM)
        go func() {
            <-c
            os.Exit(0)
        }()
    }()

    fmt.Printf("HTTP proxy listening at %s\n", opts.ListenAddr)
    return p.Serve(l)
}
```

---

### 5. http/modifier.go

**修改结构体**

```go
// 修改前
type ScfModifier struct {
    apis   []string
    length int
    port   string
}

func NewScfModifier(apis []string, lport string) (*ScfModifier, error) {
    length := len(apis)
    return &ScfModifier{apis: apis, length: length, port: lport}, nil
}

func (m *ScfModifier) pickRandomApi() string {
    n := rand.Intn(m.length)
    return m.apis[n]
}

// 修改后
type ScfModifier struct {
    apiUrl string
    port   string
}

func NewScfModifier(apiUrl string, lport string) (*ScfModifier, error) {
    if apiUrl == "" {
        return nil, errors.New("api URL is required")
    }
    return &ScfModifier{apiUrl: apiUrl, port: lport}, nil
}

// 删除 pickRandomApi() 函数
```

**修改ModifyRequest函数**

```go
// 修改前
func (m *ScfModifier) ModifyRequest(req *http.Request) error {
    // ... 前面代码保持不变 ...

    scfApi := m.pickRandomApi()
    logrus.Debugf("%s - %s", req.URL, scfApi)
    scfReq, err := http.NewRequest("POST", scfApi, bytes.NewReader(data))
    *req = *scfReq

    return nil
}

// 修改后
func (m *ScfModifier) ModifyRequest(req *http.Request) error {
    // ... 前面代码保持不变 ...

    logrus.Debugf("%s - %s", req.URL, m.apiUrl)
    scfReq, err := http.NewRequest("POST", m.apiUrl, bytes.NewReader(data))
    *req = *scfReq

    return nil
}
```

**移除rand导入（如果不再使用）**

---

### 6. sdk/provider/alibaba/http.go

**修改函数规格**

```go
func (p *Provider) createHttpFunction(serviceName, functionName string) error {
    h := &fcopen.CreateFunctionHeaders{}
    r := &fcopen.CreateFunctionRequest{
        FunctionName: tea.String(functionName),
        Runtime:      tea.String("python3.9"),
        Handler:      tea.String("index.handler"),
        Timeout:      tea.Int32(120),           // 10 → 120
        MemorySize:   tea.Int32(128),          // 保持不变
        Cpu:          tea.Float32(0.05),       // 新增
        DiskSize:     tea.Int32(512),          // 新增
        Code: &fcopen.Code{
            ZipFile: tea.String(function.AlibabaHttpCodeZip),
        },
    }

    _, err := p.fclient.CreateFunctionWithOptions(tea.String(serviceName), r, h, p.runtime)
    if err != nil {
        if err, ok := err.(*tea.SDKError); !ok || *err.StatusCode != 409 {
            return err
        }
    }
    return nil
}
```

---

### 7. sdk/provider/tencent/http.go

**修改函数规格**

```go
func (p *Provider) createHttpFunction(namespace, functionName string) error {
    r := scf.NewCreateFunctionRequest()
    r.Namespace = common.StringPtr(namespace)
    r.FunctionName = common.StringPtr(functionName)
    r.Code = &scf.Code{ZipFile: common.StringPtr(function.TencentHttpCodeZip)}
    r.Handler = common.StringPtr("index.handler")
    r.MemorySize = common.Int64Ptr(128)      // 保持不变
    r.Timeout = common.Int64Ptr(120)         // 10 → 120
    r.Runtime = common.StringPtr("Python3.6")

    _, err := p.fclient.CreateFunction(r)
    if err != nil {
        if err, ok := err.(*errors.TencentCloudSDKError); !ok || err.Code != scf.RESOURCEINUSE_FUNCTION {
            return err
        }
    }
    return nil
}
```

---

### 8. sdk/provider/aws/http.go

**修改函数规格**

```go
func (p *Provider) createHttpFunction(functionName string) error {
    input := &lambda.CreateFunctionInput{
        FunctionName:  aws.String(functionName),
        Code:          &types.FunctionCode{ZipFile: []byte(function.AwsHttpCodeZip)},
        Handler:       aws.String("index.handler"),
        MemorySize:    aws.Int32(128),              // 保持不变
        Architectures: []types.Architecture{types.ArchitectureArm64},
        Timeout:       aws.Int32(120),              // 10 → 120
        Runtime:       types.RuntimePython39,
        PackageType:   types.PackageTypeZip,
        Role:          p.roleArn,
    }

    _, err := p.fclient.CreateFunction(p.ctx, input)
    return err
}
```

---

## 使用示例

```bash
# 部署多个云函数
scfproxy deploy http -p alibaba,tencent -r cn-hangzhou,cn-shenzhen

# 启动代理（自动开启9900, 9901, 9902, 9903...）
scfproxy http -l :9900

# 使用代理
curl -x http://127.0.0.1:9900 https://example.com
curl -x http://127.0.0.1:9901 https://example.com
```

---

## 注意事项

1. **部署顺序**: 配置文件中URL的顺序取决于并发写入完成顺序，非严格按provider-region顺序
2. **端口分配**: 从指定基础端口开始递增（如:9900 → 9900, 9901, 9902...）
3. **云平台差异**: 腾讯云和AWS不支持单独设置CPU和DiskSize参数
4. **证书管理**: 所有端口使用相同的CA证书
5. **向后兼容**: 旧版本配置文件需要手动清理或迁移
