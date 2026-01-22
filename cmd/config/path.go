package config

import (
	"os"
)

var (
	// 使用程序当前目录下的 config 目录
	CertPath           = "config/scfproxy.cer"
	KeyPath            = "config/scfproxy.key"
	HttpProxyPath      = "config/http.json"
	SocksProxyPath     = "config/socks.json"
	ReverseProxyPath   = "config/reverse.json"
	ProviderConfigPath = "config/sdk.toml"
)

func init() {
	os.MkdirAll("config", os.ModePerm)
}

// GetConfigDir 获取配置文件所在目录（程序当前目录/config）
func GetConfigDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return cwd + "/config", nil
}
