package cmd

import (
	"testing"

	"github.com/shimmeris/SCFProxy/cmd/config"
)

func TestShouldSkipHttpDeploy(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		record   *config.HttpRecord
		want     bool
	}{
		{
			name:     "non tencent deployed record",
			provider: "aws",
			record:   &config.HttpRecord{Url: "https://example.com"},
			want:     true,
		},
		{
			name:     "legacy tencent record should redeploy",
			provider: "tencent",
			record:   &config.HttpRecord{Url: "https://service-abc.gz.apigw.tencentcs.com/release/http_trigger"},
			want:     false,
		},
		{
			name:     "function url tencent record should skip",
			provider: "tencent",
			record: &config.HttpRecord{
				Url:  "https://service-12345.ap-beijing.tencentscf.com",
				Kind: config.HttpRecordKindFunctionURL,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldSkipHttpDeploy(tt.provider, tt.record); got != tt.want {
				t.Fatalf("shouldSkipHttpDeploy() = %v, want %v", got, tt.want)
			}
		})
	}
}
