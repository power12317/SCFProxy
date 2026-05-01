package sdk

type FunctionOpts struct {
	Namespace    string
	FunctionName string
	TriggerName  string
	OnlyTrigger  bool
	SecretKey    string // 全局暗号
}

type HttpConnectFunctionOpts struct {
	Namespace    string
	FunctionName string
	TriggerName  string
	OnlyTrigger  bool
	SecretKey    string
	Timeout      int64
	IdleTimeout  uint64
}

type ReverseProxyOpts struct {
	Origin    string
	ServiceId string
	ApiId     string
	PluginId  string
	Ips       []string
}
