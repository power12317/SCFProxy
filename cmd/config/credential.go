package config

import (
	"os"

	"github.com/pelletier/go-toml"
)

const ProviderConfigContent = `[global]
# 全局暗号，用于验证云函数请求
# 留空则不启用验证（不推荐）
secret_key = ""

[alibaba]
AccessKeyId = ""
AccessKeySecret = ""
AccountId = ""

[aws]
AccessKeyId = ""
AccessKeySecret = ""
RoleArn = ""

[tencent]
# Named SecretId in tencent
AccessKeyId = ""
# Named SecretKey in tencent
AccessKeySecret = ""

[huawei]
AccessKeyId = ""
AccessKeySecret = ""
`

type Credential struct {
	AccessKeyId     string `toml:"AccessKeyId"`
	AccessKeySecret string `toml:"AccessKeySecret"`
	AccountId       string `toml:"AccountId"`
	RoleArn         string `toml:"RoleArn"`
}

func (c Credential) isSet() bool {
	return c.AccessKeyId != "" && c.AccessKeySecret != ""
}

type ProviderConfig struct {
	Global  *GlobalConfig `toml:"global"`
	Alibaba *Credential   `toml:"alibaba"`
	Tencent *Credential   `toml:"tencent"`
	Aws     *Credential   `toml:"aws"`
}

type GlobalConfig struct {
	SecretKey string `toml:"secret_key"`
}

func LoadProviderConfig(path string) (*ProviderConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	config := &ProviderConfig{}
	if err := toml.Unmarshal(data, config); err != nil {
		return nil, err
	}
	return config, nil
}

func (c *ProviderConfig) ProviderCredentialByName(provider string) *Credential {
	switch provider {
	case "alibaba":
		return c.Alibaba
	case "tencent":
		return c.Tencent
	case "aws":
		return c.Aws
	default:
		return nil
	}
}

func (c *ProviderConfig) IsSet(provider string) bool {
	cred := c.ProviderCredentialByName(provider)
	if cred == nil {
		return false
	}
	return cred.isSet()
}

// GetSecretKey 获取全局暗号
func (c *ProviderConfig) GetSecretKey() string {
	if c.Global == nil {
		return ""
	}
	return c.Global.SecretKey
}
