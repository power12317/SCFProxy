package config

import "testing"

func TestHttpRecordIsTencentFunctionURL(t *testing.T) {
	tests := []struct {
		name   string
		record *HttpRecord
		want   bool
	}{
		{
			name: "function url record",
			record: &HttpRecord{
				Url:  "https://service-12345.ap-beijing.tencentscf.com",
				Kind: HttpRecordKindFunctionURL,
			},
			want: true,
		},
		{
			name: "legacy api gateway record",
			record: &HttpRecord{
				Url:  "https://service-abc.gz.apigw.tencentcs.com/release/http_trigger",
				Kind: HttpRecordKindFunctionURL,
			},
			want: false,
		},
		{
			name: "missing kind",
			record: &HttpRecord{
				Url: "https://service-12345.ap-beijing.tencentscf.com",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.record.IsTencentFunctionURL(); got != tt.want {
				t.Fatalf("IsTencentFunctionURL() = %v, want %v", got, tt.want)
			}
		})
	}
}
