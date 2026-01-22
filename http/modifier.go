package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/google/martian/v3"
	"github.com/sirupsen/logrus"
)

type ScfModifier struct {
	apiUrl    string
	secretKey string
	port      string
}

type httpRequest struct {
	Method string            `json:"method"`
	Url    string            `json:"url"`
	Header map[string]string `json:"headers"`
	Body   string            `json:"body"`
}

type httpResponse struct {
	Url    string                 `json:"url"`
	Code   int                    `json:"status_code"`
	Header map[string]interface{} `json:"headers"` // 支持字符串和数组
	Body   string                 `json:"content"`
}

func NewScfModifier(apiUrl, secretKey string, lport string) (*ScfModifier, error) {
	if apiUrl == "" {
		return nil, errors.New("api URL is required")
	}
	return &ScfModifier{apiUrl: apiUrl, secretKey: secretKey, port: lport}, nil
}

func (m *ScfModifier) ModifyRequest(req *http.Request) error {
	// Prevent scfproxy from recursively connecting to itself.
	remoteIp, _, _ := net.SplitHostPort(req.RemoteAddr)
	if remoteIp == req.URL.Hostname() && m.port == req.URL.Port() {
		ctx := martian.NewContext(req)
		ctx.SkipRoundTrip()
		return errors.New("Detecting recursive connection")
	}

	if req.Method == http.MethodConnect {
		return nil
	}

	headers := make(map[string]string)
	for k := range req.Header {
		headers[k] = strings.Join(req.Header.Values(k), ",")
	}

	rawBody, err := io.ReadAll(req.Body)
	if err != nil {
		logrus.Debugf("Error reading request body")
		return err
	}
	req.Body.Close()
	base64Body := base64.StdEncoding.EncodeToString(rawBody)

	hr := httpRequest{Method: req.Method, Url: req.URL.String(), Header: headers, Body: base64Body}
	data, err := json.Marshal(hr)
	if err != nil {
		return err
	}

	logrus.Debugf("%s - %s", req.URL, m.apiUrl)
	scfReq, err := http.NewRequest("POST", m.apiUrl, bytes.NewReader(data))

	// 添加暗号 Header
	if m.secretKey != "" {
		scfReq.Header.Set("X-SCF-Secret-Key", m.secretKey)
	}

	*req = *scfReq

	return nil
}

func (m *ScfModifier) ModifyResponse(res *http.Response) error {
	if res.Request.Method == http.MethodConnect {
		return nil
	}

	rawBody, err := io.ReadAll(res.Body)
	res.Body.Close()

	var hr httpResponse
	err = json.Unmarshal(rawBody, &hr)
	if err != nil {
		logrus.Debugf("Error Unmarshaling %s", string(rawBody))
		return err
	}

	res.StatusCode = hr.Code
	res.Status = fmt.Sprintf("%d %s", hr.Code, http.StatusText(hr.Code))

	res.Header = http.Header{}
	for k, v := range hr.Header {
		// 检查值类型，可能是字符串或数组
		switch val := v.(type) {
		case string:
			// 单个值，使用 Set
			res.Header.Set(k, val)
		case []interface{}:
			// 多个值（如多个 set-cookie），逐个添加
			for _, item := range val {
				if strItem, ok := item.(string); ok {
					res.Header.Add(k, strItem)
				}
			}
		}
	}

	body, err := base64.StdEncoding.DecodeString(hr.Body)
	if err != nil {
		logrus.Debugf("Error decoding base64 %s", hr.Body)
		return err
	}
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))

	return nil
}
