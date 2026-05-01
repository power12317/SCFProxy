package tencent

import (
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerrors "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	scf "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/scf/v20180416"

	"github.com/shimmeris/SCFProxy/function"
	"github.com/shimmeris/SCFProxy/sdk"
)

const (
	tencentHTTPConnectMemorySize = 64
	tencentHTTPConnectRuntime    = "Go1"
)

func (p *Provider) DeployHttpConnectProxy(opts *sdk.HttpConnectFunctionOpts) (string, error) {
	p.secretKey = opts.SecretKey

	if opts.Timeout == 0 {
		opts.Timeout = 900
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 600
	}

	if err := p.createNamespace(opts.Namespace); err != nil {
		return "", err
	}

	if err := p.ensureHttpConnectFunction(opts); err != nil {
		return "", err
	}
	if err := p.waitForFunctionReady(opts.Namespace, opts.FunctionName, tencentFunctionReadyTimeout); err != nil {
		return "", err
	}

	return p.ensureHttpTrigger(opts.Namespace, opts.FunctionName, opts.TriggerName)
}

func (p *Provider) ClearHttpConnectProxy(opts *sdk.HttpConnectFunctionOpts) error {
	if err := p.deleteTrigger(opts.Namespace, opts.FunctionName, opts.TriggerName, tencentFunctionURLTriggerType); err != nil {
		return err
	}
	if opts.OnlyTrigger {
		return nil
	}
	return p.deleteFunction(opts.Namespace, opts.FunctionName)
}

func (p *Provider) ensureHttpConnectFunction(opts *sdk.HttpConnectFunctionOpts) error {
	if err := p.createHttpConnectFunction(opts); err != nil {
		var sdkErr *tcerrors.TencentCloudSDKError
		if !errors.As(err, &sdkErr) || sdkErr.Code != scf.RESOURCEINUSE_FUNCTION {
			return err
		}

		if err := p.waitForFunctionReady(opts.Namespace, opts.FunctionName, tencentFunctionReadyTimeout); err != nil {
			return err
		}
		if err := p.updateHttpConnectFunctionCode(opts); err != nil {
			return err
		}
		if err := p.waitForFunctionReady(opts.Namespace, opts.FunctionName, tencentFunctionReadyTimeout); err != nil {
			return err
		}
		if err := p.updateHttpConnectFunctionConfiguration(opts); err != nil {
			return err
		}
		if err := p.waitForFunctionReady(opts.Namespace, opts.FunctionName, tencentFunctionReadyTimeout); err != nil {
			return err
		}
		return nil
	}

	return p.waitForFunctionReady(opts.Namespace, opts.FunctionName, tencentFunctionReadyTimeout)
}

func (p *Provider) createHttpConnectFunction(opts *sdk.HttpConnectFunctionOpts) error {
	r := scf.NewCreateFunctionRequest()
	r.Namespace = common.StringPtr(opts.Namespace)
	r.FunctionName = common.StringPtr(opts.FunctionName)
	r.Code = &scf.Code{ZipFile: common.StringPtr(function.TencentHttpConnectCodeZip)}
	r.MemorySize = common.Int64Ptr(tencentHTTPConnectMemorySize)
	r.Timeout = common.Int64Ptr(opts.Timeout)
	r.Runtime = common.StringPtr(tencentHTTPConnectRuntime)
	r.Type = common.StringPtr("HTTP")
	r.ProtocolType = common.StringPtr("WS")
	r.ProtocolParams = &scf.ProtocolParams{WSParams: &scf.WSParams{IdleTimeOut: common.Uint64Ptr(opts.IdleTimeout)}}
	r.ClsLogsetId = common.StringPtr("")
	r.ClsTopicId = common.StringPtr("")
	if env := buildTencentEnvironment(p.secretKey); env != nil {
		r.Environment = env
	}

	_, err := p.fclient.CreateFunction(r)
	return err
}

func (p *Provider) updateHttpConnectFunctionCode(opts *sdk.HttpConnectFunctionOpts) error {
	var lastErr error
	for i := 0; i < 5; i++ {
		r := scf.NewUpdateFunctionCodeRequest()
		r.Namespace = common.StringPtr(opts.Namespace)
		r.FunctionName = common.StringPtr(opts.FunctionName)
		r.ZipFile = common.StringPtr(function.TencentHttpConnectCodeZip)

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

func (p *Provider) updateHttpConnectFunctionConfiguration(opts *sdk.HttpConnectFunctionOpts) error {
	var lastErr error
	for i := 0; i < 5; i++ {
		r := scf.NewUpdateFunctionConfigurationRequest()
		r.Namespace = common.StringPtr(opts.Namespace)
		r.FunctionName = common.StringPtr(opts.FunctionName)
		r.MemorySize = common.Int64Ptr(tencentHTTPConnectMemorySize)
		r.Timeout = common.Int64Ptr(opts.Timeout)
		r.Runtime = common.StringPtr(tencentHTTPConnectRuntime)
		r.ProtocolParams = &scf.ProtocolParams{WSParams: &scf.WSParams{IdleTimeOut: common.Uint64Ptr(opts.IdleTimeout)}}
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
	if lastErr == nil {
		lastErr = fmt.Errorf("failed to update tencent http-connect function %s.%s", p.region, opts.FunctionName)
	}
	logrus.Debug(lastErr)
	return lastErr
}
