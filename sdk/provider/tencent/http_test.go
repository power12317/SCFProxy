package tencent

import "testing"

func TestExtractTencentFunctionURL(t *testing.T) {
	tests := []struct {
		name string
		desc string
		want string
	}{
		{
			name: "raw string url",
			desc: `{"Url":"https://service-12345.ap-beijing.tencentscf.com/default/scf_http"}`,
			want: "https://service-12345.ap-beijing.tencentscf.com/default/scf_http",
		},
		{
			name: "nested list url",
			desc: `{"meta":{"endpoints":["https://service-12345.ap-beijing.tencentscf.com"]}}`,
			want: "https://service-12345.ap-beijing.tencentscf.com",
		},
		{
			name: "no url",
			desc: `{"AuthType":"NONE","Methods":"POST"}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractTencentFunctionURL(tt.desc); got != tt.want {
				t.Fatalf("extractTencentFunctionURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
