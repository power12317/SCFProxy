package alibaba

import (
	"fmt"
	"time"

	fcopen "github.com/alibabacloud-go/fc-open-20210406/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/sirupsen/logrus"

	"github.com/shimmeris/SCFProxy/function"
	"github.com/shimmeris/SCFProxy/sdk"
)

const (
	alibabaHTTPConnectRuntime              = "custom.debian10"
	alibabaHTTPConnectCAPort               = 9000
	alibabaHTTPConnectMemorySize           = 128
	alibabaHTTPConnectCPU          float32 = 0.05
	alibabaHTTPConnectDiskSize             = 512
	alibabaHTTPConnectConcurrency          = 100
	alibabaHTTPConnectSecretEnvKey         = "SCF_SECRET_KEY"
)

func (p *Provider) DeployHttpConnectProxy(opts *sdk.HttpConnectFunctionOpts) (string, error) {
	p.secretKey = opts.SecretKey

	if opts.Timeout == 0 {
		opts.Timeout = 900
	}

	if err := p.createService(opts.Namespace); err != nil {
		return "", err
	}
	if err := p.ensureHttpConnectFunction(opts); err != nil {
		return "", err
	}
	return p.ensureHttpConnectTrigger(opts.Namespace, opts.FunctionName, opts.TriggerName)
}

func (p *Provider) ClearHttpConnectProxy(opts *sdk.HttpConnectFunctionOpts) error {
	if err := p.deleteTrigger(opts.Namespace, opts.FunctionName, opts.TriggerName); err != nil {
		return err
	}
	if opts.OnlyTrigger {
		return nil
	}
	return p.deleteFunction(opts.Namespace, opts.FunctionName)
}

func (p *Provider) ensureHttpConnectFunction(opts *sdk.HttpConnectFunctionOpts) error {
	if err := p.createHttpConnectFunction(opts); err != nil {
		if !isAlibabaStatus(err, 409) {
			return err
		}
		return p.updateHttpConnectFunction(opts)
	}
	return nil
}

func (p *Provider) createHttpConnectFunction(opts *sdk.HttpConnectFunctionOpts) error {
	h := &fcopen.CreateFunctionHeaders{}
	r := p.newHttpConnectFunctionRequest(opts)
	_, err := p.fclient.CreateFunctionWithOptions(tea.String(opts.Namespace), r, h, p.runtime)
	return err
}

func (p *Provider) updateHttpConnectFunction(opts *sdk.HttpConnectFunctionOpts) error {
	h := &fcopen.UpdateFunctionHeaders{}
	r := &fcopen.UpdateFunctionRequest{
		Runtime:             tea.String(alibabaHTTPConnectRuntime),
		Handler:             tea.String("bootstrap"),
		Timeout:             tea.Int32(int32(opts.Timeout)),
		MemorySize:          tea.Int32(alibabaHTTPConnectMemorySize),
		Cpu:                 tea.Float32(alibabaHTTPConnectCPU),
		DiskSize:            tea.Int32(alibabaHTTPConnectDiskSize),
		CaPort:              tea.Int32(alibabaHTTPConnectCAPort),
		InstanceConcurrency: tea.Int32(alibabaHTTPConnectConcurrency),
		Code:                &fcopen.Code{ZipFile: tea.String(function.AlibabaHttpConnectCodeZip)},
		EnvironmentVariables: map[string]*string{
			alibabaHTTPConnectSecretEnvKey: tea.String(p.secretKey),
		},
	}

	_, err := p.fclient.UpdateFunctionWithOptions(tea.String(opts.Namespace), tea.String(opts.FunctionName), r, h, p.runtime)
	return err
}

func (p *Provider) newHttpConnectFunctionRequest(opts *sdk.HttpConnectFunctionOpts) *fcopen.CreateFunctionRequest {
	return &fcopen.CreateFunctionRequest{
		FunctionName:        tea.String(opts.FunctionName),
		Runtime:             tea.String(alibabaHTTPConnectRuntime),
		Handler:             tea.String("bootstrap"),
		Timeout:             tea.Int32(int32(opts.Timeout)),
		MemorySize:          tea.Int32(alibabaHTTPConnectMemorySize),
		Cpu:                 tea.Float32(alibabaHTTPConnectCPU),
		DiskSize:            tea.Int32(alibabaHTTPConnectDiskSize),
		CaPort:              tea.Int32(alibabaHTTPConnectCAPort),
		InstanceConcurrency: tea.Int32(alibabaHTTPConnectConcurrency),
		Code:                &fcopen.Code{ZipFile: tea.String(function.AlibabaHttpConnectCodeZip)},
		EnvironmentVariables: map[string]*string{
			alibabaHTTPConnectSecretEnvKey: tea.String(p.secretKey),
		},
	}
}

func (p *Provider) ensureHttpConnectTrigger(serviceName, functionName, triggerName string) (string, error) {
	// Recreate the trigger so deployments always enforce GET support required by
	// WebSocket handshakes.
	if err := p.deleteTrigger(serviceName, functionName, triggerName); err != nil {
		return "", err
	}

	var lastErr error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(5 * time.Second)
		}

		api, err := p.createHttpConnectTrigger(serviceName, functionName, triggerName)
		if err == nil && api != "" {
			return api, nil
		}
		if err == nil {
			api, err = p.lookupHttpConnectTriggerURL(serviceName, functionName, triggerName)
			if err == nil && api != "" {
				return api, nil
			}
			if err == nil {
				err = fmt.Errorf("alibaba http-connect trigger url not found for %s.%s", p.region, functionName)
			}
		}
		lastErr = err
		logrus.Errorf("Failed creating http-connect trigger in alibaba.%s, retry after 5 sec", p.region)
	}

	return "", lastErr
}

func (p *Provider) createHttpConnectTrigger(serviceName, functionName, triggerName string) (string, error) {
	h := &fcopen.CreateTriggerHeaders{}
	r := &fcopen.CreateTriggerRequest{
		TriggerType:   tea.String("http"),
		TriggerName:   tea.String(triggerName),
		TriggerConfig: tea.String(`{"authType":"anonymous","methods":["GET"]}`),
	}

	res, err := p.fclient.CreateTriggerWithOptions(tea.String(serviceName), tea.String(functionName), r, h, p.runtime)
	if err != nil {
		return "", err
	}
	if res != nil && res.Body != nil && res.Body.UrlInternet != nil {
		return tea.StringValue(res.Body.UrlInternet), nil
	}
	return "", nil
}

func (p *Provider) lookupHttpConnectTriggerURL(serviceName, functionName, triggerName string) (string, error) {
	h := &fcopen.ListTriggersHeaders{}
	r := &fcopen.ListTriggersRequest{Limit: tea.Int32(100)}
	res, err := p.fclient.ListTriggersWithOptions(tea.String(serviceName), tea.String(functionName), r, h, p.runtime)
	if err != nil {
		return "", err
	}
	if res == nil || res.Body == nil {
		return "", nil
	}
	for _, trigger := range res.Body.Triggers {
		if trigger == nil || trigger.TriggerName == nil || trigger.TriggerType == nil {
			continue
		}
		if tea.StringValue(trigger.TriggerName) != triggerName || tea.StringValue(trigger.TriggerType) != "http" {
			continue
		}
		if trigger.UrlInternet != nil {
			return tea.StringValue(trigger.UrlInternet), nil
		}
	}
	return "", nil
}

func isAlibabaStatus(err error, status int) bool {
	sdkErr, ok := err.(*tea.SDKError)
	return ok && sdkErr.StatusCode != nil && tea.IntValue(sdkErr.StatusCode) == status
}
