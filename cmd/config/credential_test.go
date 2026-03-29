package config

import (
	"testing"

	"github.com/pelletier/go-toml"
)

func TestLoadProviderConfigReadsGlobalSecretKey(t *testing.T) {
	data := []byte(`
[global]
secret_key = "test_secret_value"

[tencent]
AccessKeyId = "id"
AccessKeySecret = "secret"
`)

	conf := &ProviderConfig{}
	if err := toml.Unmarshal(data, conf); err != nil {
		t.Fatalf("toml.Unmarshal() error = %v", err)
	}

	if conf.Global == nil {
		t.Fatalf("Global config is nil")
	}
	if got, want := conf.Global.SecretKey, "test_secret_value"; got != want {
		t.Fatalf("SecretKey = %q, want %q", got, want)
	}
	if conf.Tencent == nil {
		t.Fatalf("Tencent config is nil")
	}
	if got, want := conf.Tencent.AccessKeyId, "id"; got != want {
		t.Fatalf("Tencent.AccessKeyId = %q, want %q", got, want)
	}
}
