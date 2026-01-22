package cmd

import (
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/shimmeris/SCFProxy/cmd/config"
	"github.com/shimmeris/SCFProxy/http"
)

var (
	listenAddr string
	certPath   string
	keyPath    string
)

var httpCmd = &cobra.Command{
	Use:   "http",
	Short: "Start http proxy",
	RunE: func(cmd *cobra.Command, args []string) error {
		conf, err := config.LoadHttpConfig()
		if err != nil {
			return err
		}

		// 加载全局配置获取暗号
		providerConfig, err := config.LoadProviderConfig(config.ProviderConfigPath)
		if err != nil {
			return err
		}
		secretKey := providerConfig.GetSecretKey()

		// 获取所有已部署的记录（包含 provider、region 和端口信息）
		records := conf.GetAllRecordsWithInfo()
		if len(records) == 0 {
			return errors.New("no deployed functions found")
		}

		// 解析基础端口和主机
		host, portStr, err := net.SplitHostPort(listenAddr)
		if err != nil {
			return err
		}
		basePort, _ := strconv.Atoi(portStr)

		// 收集端口映射信息用于日志
		type PortMapping struct {
			Port     int
			Provider string
			Region   string
		}
		portMappings := []PortMapping{}

		// 启动所有端口
		for _, record := range records {
			// 如果记录中有指定的 Port，使用它；否则使用 basePort + 索引
			port := record.Port
			if port == 0 {
				// Port 为 0 表示未指定，使用顺序分配
				port = basePort + len(portMappings)
			}

			portMappings = append(portMappings, PortMapping{
				Port:     port,
				Provider: record.Provider,
				Region:   record.Region,
			})

			go func(port int, apiUrl string, secretKey string, provider string, region string) {
				opts := &http.Options{
					ListenAddr: fmt.Sprintf("%s:%d", host, port),
					CertPath:   certPath,
					KeyPath:    keyPath,
					ApiUrl:     apiUrl,
					SecretKey:  secretKey,
				}
				if err := http.ServeProxy(opts); err != nil {
					logrus.Errorf("Port %d (%s.%s) failed: %v", port, provider, region, err)
				}
			}(port, record.Url, secretKey, record.Provider, record.Region)
		}

		// 输出详细的端口映射信息
		logrus.Infof("Started %d HTTP proxies:", len(portMappings))
		for _, mapping := range portMappings {
			logrus.Infof("  Port %d -> %s.%s", mapping.Port, mapping.Provider, mapping.Region)
		}

		// 阻塞主线程
		select {}
	},
}

func init() {
	rootCmd.AddCommand(httpCmd)
	httpCmd.Flags().StringVarP(&listenAddr, "listen", "l", "", "host:port of the proxy (base port)")
	httpCmd.Flags().StringVarP(&certPath, "certPath", "c", config.CertPath, "filepath to the CA certificate")
	httpCmd.Flags().StringVarP(&keyPath, "keyPath", "k", config.KeyPath, "filepath to the private key")

	httpCmd.MarkFlagRequired("listen")
}
