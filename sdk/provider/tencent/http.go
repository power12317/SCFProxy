package tencent

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	scf "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/scf/v20180416"

	"github.com/shimmeris/SCFProxy/function"
	"github.com/shimmeris/SCFProxy/sdk"
)

const (
	tencentFunctionURLTriggerType = "http"
	tencentLegacyAPIGWTriggerType = "apigw"
	tencentFunctionReadyTimeout   = 2 * time.Minute
	tencentFunctionReadyInterval  = 3 * time.Second
	tencentSecretEnvKey           = "APP_AUTH_TOKEN"
	tencentHTTPMemorySize         = 64
)

var tencentTriggerURLPattern = regexp.MustCompile(`https://[^\s"']+\.tencentscf\.com[^\s"']*`)

func (p *Provider) DeployHttpProxy(opts *sdk.FunctionOpts) (string, error) {
	p.secretKey = opts.SecretKey

	if err := p.createNamespace(opts.Namespace); err != nil {
		return "", err
	}

	if err := p.ensureHttpFunction(opts.Namespace, opts.FunctionName); err != nil {
		return "", err
	}
	if err := p.waitForFunctionReady(opts.Namespace, opts.FunctionName, tencentFunctionReadyTimeout); err != nil {
		return "", err
	}

	api, err := p.ensureHttpTrigger(opts.Namespace, opts.FunctionName, opts.TriggerName)
	if err != nil {
		return "", err
	}

	return api, nil
}

func (p *Provider) ClearHttpProxy(opts *sdk.FunctionOpts) error {
	if err := p.deleteTrigger(opts.Namespace, opts.FunctionName, opts.TriggerName, tencentFunctionURLTriggerType); err != nil {
		return err
	}

	// Best-effort cleanup for legacy API gateway deployments.
	if err := p.deleteTrigger(opts.Namespace, opts.FunctionName, opts.TriggerName, tencentLegacyAPIGWTriggerType); err != nil {
		return err
	}

	if opts.OnlyTrigger {
		return nil
	}

	return p.deleteFunction(opts.Namespace, opts.FunctionName)
}

func (p *Provider) createNamespace(namespace string) error {
	r := scf.NewCreateNamespaceRequest()
	r.Namespace = common.StringPtr(namespace)

	_, err := p.fclient.CreateNamespace(r)
	if err != nil {
		if err, ok := err.(*tcerrors.TencentCloudSDKError); !ok || err.Code != scf.RESOURCEINUSE_NAMESPACE {
			return err
		}
	}
	return nil
}

func (p *Provider) ensureHttpFunction(namespace, functionName string) error {
	if err := p.createHttpFunction(namespace, functionName); err != nil {
		var sdkErr *tcerrors.TencentCloudSDKError
		if !errors.As(err, &sdkErr) || sdkErr.Code != scf.RESOURCEINUSE_FUNCTION {
			return err
		}

		if err := p.waitForFunctionReady(namespace, functionName, tencentFunctionReadyTimeout); err != nil {
			return err
		}
		if err := p.updateHttpFunctionCode(namespace, functionName); err != nil {
			return err
		}
		if err := p.waitForFunctionReady(namespace, functionName, tencentFunctionReadyTimeout); err != nil {
			return err
		}
		if err := p.updateHttpFunctionConfiguration(namespace, functionName); err != nil {
			return err
		}
		if err := p.waitForFunctionReady(namespace, functionName, tencentFunctionReadyTimeout); err != nil {
			return err
		}
		return nil
	}

	return p.waitForFunctionReady(namespace, functionName, tencentFunctionReadyTimeout)
}

func (p *Provider) createHttpFunction(namespace, functionName string) error {
	r := scf.NewCreateFunctionRequest()
	r.Namespace = common.StringPtr(namespace)
	r.FunctionName = common.StringPtr(functionName)
	r.Code = &scf.Code{ZipFile: common.StringPtr(function.TencentHttpCodeZip)}
	r.Handler = common.StringPtr("index.handler")
	r.MemorySize = common.Int64Ptr(tencentHTTPMemorySize)
	r.Timeout = common.Int64Ptr(120)
	r.Runtime = common.StringPtr("Python3.6")
	r.ClsLogsetId = common.StringPtr("")
	r.ClsTopicId = common.StringPtr("")
	if env := buildTencentEnvironment(p.secretKey); env != nil {
		r.Environment = env
	}

	_, err := p.fclient.CreateFunction(r)
	return err
}

func (p *Provider) updateHttpFunctionCode(namespace, functionName string) error {
	var lastErr error
	for i := 0; i < 5; i++ {
		r := scf.NewUpdateFunctionCodeRequest()
		r.Namespace = common.StringPtr(namespace)
		r.FunctionName = common.StringPtr(functionName)
		r.Handler = common.StringPtr("index.handler")
		r.ZipFile = common.StringPtr(function.TencentHttpCodeZip)

		_, err := p.fclient.UpdateFunctionCode(r)
		if err == nil {
			return nil
		}
		if !isTencentFunctionBusyError(err) {
			return err
		}
		lastErr = err
		time.Sleep(tencentFunctionReadyInterval)
	}

	return lastErr
}

func (p *Provider) updateHttpFunctionConfiguration(namespace, functionName string) error {
	var lastErr error
	for i := 0; i < 5; i++ {
		r := scf.NewUpdateFunctionConfigurationRequest()
		r.Namespace = common.StringPtr(namespace)
		r.FunctionName = common.StringPtr(functionName)
		r.MemorySize = common.Int64Ptr(tencentHTTPMemorySize)
		r.Timeout = common.Int64Ptr(120)
		r.Runtime = common.StringPtr("Python3.6")
		r.ClsLogsetId = common.StringPtr("")
		r.ClsTopicId = common.StringPtr("")
		if env := buildTencentEnvironment(p.secretKey); env != nil {
			r.Environment = env
		}

		_, err := p.fclient.UpdateFunctionConfiguration(r)
		if err == nil {
			return nil
		}
		if !isTencentFunctionBusyError(err) {
			return err
		}
		lastErr = err
		time.Sleep(tencentFunctionReadyInterval)
	}

	return lastErr
}

func buildTencentEnvironment(secretKey string) *scf.Environment {
	if secretKey == "" {
		return nil
	}

	return &scf.Environment{
		Variables: []*scf.Variable{
			{
				Key:   common.StringPtr(tencentSecretEnvKey),
				Value: common.StringPtr(secretKey),
			},
		},
	}
}

func (p *Provider) waitForFunctionReady(namespace, functionName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		ready, status, err := p.isFunctionReady(namespace, functionName)
		if err == nil && ready {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for tencent function %s.%s to become ready, last status: %s", p.region, functionName, status)
		}
		logrus.Debugf("Waiting for tencent function %s.%s to become ready, current status: %s", p.region, functionName, status)
		time.Sleep(tencentFunctionReadyInterval)
	}
}

func (p *Provider) isFunctionReady(namespace, functionName string) (bool, string, error) {
	r := scf.NewGetFunctionRequest()
	r.Namespace = common.StringPtr(namespace)
	r.FunctionName = common.StringPtr(functionName)

	resp, err := p.fclient.GetFunction(r)
	if err != nil {
		return false, "", err
	}
	if resp == nil || resp.Response == nil || resp.Response.Status == nil {
		return false, "", nil
	}

	status := strings.TrimSpace(*resp.Response.Status)
	switch status {
	case "", "Active":
		return true, status, nil
	case "Creating", "Updating":
		return false, status, nil
	default:
		return false, status, fmt.Errorf("tencent function %s.%s is not ready, status: %s", p.region, functionName, status)
	}
}

func isTencentFunctionBusyError(err error) bool {
	var sdkErr *tcerrors.TencentCloudSDKError
	if !errors.As(err, &sdkErr) {
		return false
	}

	return sdkErr.Code == scf.FAILEDOPERATION_UPDATEFUNCTIONCONFIGURATION ||
		sdkErr.Code == scf.FAILEDOPERATION_UPDATEFUNCTIONCODE ||
		sdkErr.Code == scf.FAILEDOPERATION_FUNCTIONSTATUSERROR
}

func (p *Provider) ensureHttpTrigger(namespace, functionName, triggerName string) (string, error) {
	if err := p.deleteTrigger(namespace, functionName, triggerName, tencentLegacyAPIGWTriggerType); err != nil {
		return "", err
	}

	// Recreate the function URL trigger to enforce the desired auth/method config.
	if err := p.deleteTrigger(namespace, functionName, triggerName, tencentFunctionURLTriggerType); err != nil {
		return "", err
	}

	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(5 * time.Second)
		}

		api, err := p.createHttpTrigger(namespace, functionName, triggerName)
		if err == nil && api != "" {
			return api, nil
		}
		if err == nil {
			api, err = p.lookupHttpTriggerURL(namespace, functionName, triggerName)
			if err == nil && api != "" {
				return api, nil
			}
			if err == nil {
				err = fmt.Errorf("tencent function url not found for %s.%s", p.region, functionName)
			}
		}

		var sdkErr *tcerrors.TencentCloudSDKError
		if errors.As(err, &sdkErr) &&
			(sdkErr.Code == scf.RESOURCEINUSE || sdkErr.Code == scf.RESOURCEINUSE_TRIGGER || sdkErr.Code == scf.RESOURCEINUSE_TRIGGERNAME) {
			api, lookupErr := p.lookupHttpTriggerURL(namespace, functionName, triggerName)
			if lookupErr == nil && api != "" {
				return api, nil
			}
		}

		lastErr = err
		logrus.Errorf("Failed creating function URL in tencent.%s, retry after 5 sec", p.region)
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("failed to create tencent function url for %s.%s", p.region, functionName)
	}
	return "", lastErr
}

func (p *Provider) createHttpTrigger(namespace, functionName, triggerName string) (string, error) {
	r := scf.NewCreateTriggerRequest()
	r.FunctionName = common.StringPtr(functionName)
	r.TriggerName = common.StringPtr(triggerName)
	r.Type = common.StringPtr(tencentFunctionURLTriggerType)
	r.TriggerDesc = common.StringPtr(`{"AuthType":"NONE","NetConfig":{"EnableIntranet":false,"EnableExtranet":true}}`)
	r.Namespace = common.StringPtr(namespace)
	r.Enable = common.StringPtr("OPEN")

	response, err := p.fclient.CreateTrigger(r)
	if err != nil {
		return "", err
	}

	if response != nil && response.Response != nil && response.Response.TriggerInfo != nil && response.Response.TriggerInfo.TriggerDesc != nil {
		if api := extractTencentFunctionURL(*response.Response.TriggerInfo.TriggerDesc); api != "" {
			return api, nil
		}
	}

	return "", nil
}

func (p *Provider) lookupHttpTriggerURL(namespace, functionName, triggerName string) (string, error) {
	if api, err := p.lookupHttpTriggerURLFromList(namespace, functionName, triggerName); err != nil || api != "" {
		return api, err
	}

	return p.lookupHttpTriggerURLFromFunction(namespace, functionName)
}

func (p *Provider) lookupHttpTriggerURLFromList(namespace, functionName, triggerName string) (string, error) {
	r := scf.NewListTriggersRequest()
	r.Namespace = common.StringPtr(namespace)
	r.FunctionName = common.StringPtr(functionName)
	r.Limit = common.Uint64Ptr(20)

	resp, err := p.fclient.ListTriggers(r)
	if err != nil {
		return "", err
	}

	if resp == nil || resp.Response == nil {
		return "", nil
	}

	for _, trigger := range resp.Response.Triggers {
		if trigger == nil {
			continue
		}
		if trigger.TriggerName == nil || *trigger.TriggerName != triggerName {
			continue
		}
		if trigger.Type == nil || *trigger.Type != tencentFunctionURLTriggerType {
			continue
		}
		if trigger.TriggerDesc != nil {
			if api := extractTencentFunctionURL(*trigger.TriggerDesc); api != "" {
				return api, nil
			}
		}
	}

	return "", nil
}

func (p *Provider) lookupHttpTriggerURLFromFunction(namespace, functionName string) (string, error) {
	r := scf.NewGetFunctionRequest()
	r.Namespace = common.StringPtr(namespace)
	r.FunctionName = common.StringPtr(functionName)

	resp, err := p.fclient.GetFunction(r)
	if err != nil {
		return "", err
	}

	if resp == nil || resp.Response == nil || resp.Response.AccessInfo == nil || resp.Response.AccessInfo.Host == nil {
		return "", nil
	}

	host := strings.TrimSpace(*resp.Response.AccessInfo.Host)
	if host == "" {
		return "", nil
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host, nil
	}
	return "https://" + host, nil
}

func extractTencentFunctionURL(desc string) string {
	if desc == "" {
		return ""
	}

	if matches := tencentTriggerURLPattern.FindString(desc); matches != "" {
		return matches
	}

	var data interface{}
	if err := json.Unmarshal([]byte(desc), &data); err != nil {
		return ""
	}

	return findTencentFunctionURL(data)
}

func findTencentFunctionURL(value interface{}) string {
	switch v := value.(type) {
	case string:
		if matches := tencentTriggerURLPattern.FindString(v); matches != "" {
			return matches
		}
	case map[string]interface{}:
		for _, item := range v {
			if url := findTencentFunctionURL(item); url != "" {
				return url
			}
		}
	case []interface{}:
		for _, item := range v {
			if url := findTencentFunctionURL(item); url != "" {
				return url
			}
		}
	}

	return ""
}

func (p *Provider) deleteNamespace(namespace string) error {
	r := scf.NewDeleteNamespaceRequest()
	r.Namespace = common.StringPtr(namespace)

	_, err := p.fclient.DeleteNamespace(r)
	if err != nil {
		if err, ok := err.(*tcerrors.TencentCloudSDKError); !ok || err.Code != scf.RESOURCENOTFOUND_NAMESPACE {
			return err
		}
	}
	return nil
}

func (p *Provider) deleteFunction(namespace, functionName string) error {
	r := scf.NewDeleteFunctionRequest()
	r.Namespace = common.StringPtr(namespace)
	r.FunctionName = common.StringPtr(functionName)

	_, err := p.fclient.DeleteFunction(r)
	if err != nil {
		if err, ok := err.(*tcerrors.TencentCloudSDKError); !ok || (err.Code != scf.RESOURCENOTFOUND_NAMESPACE && err.Code != scf.RESOURCENOTFOUND_FUNCTION) {
			return err
		}
	}
	return nil
}

func (p *Provider) deleteTrigger(namespace, functionName, triggerName, triggerType string) error {
	r := scf.NewDeleteTriggerRequest()
	r.Namespace = common.StringPtr(namespace)
	r.FunctionName = common.StringPtr(functionName)
	r.TriggerName = common.StringPtr(triggerName)
	r.Type = common.StringPtr(triggerType)

	_, err := p.fclient.DeleteTrigger(r)
	if err != nil {
		if err, ok := err.(*tcerrors.TencentCloudSDKError); !ok || (err.Code != scf.RESOURCENOTFOUND && err.Code != scf.RESOURCENOTFOUND_FUNCTION && err.Code != scf.RESOURCENOTFOUND_TRIGGER) {
			return err
		}
	}
	return nil
}
