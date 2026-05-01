package cmd

import (
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/shimmeris/SCFProxy/cmd/config"
	"github.com/shimmeris/SCFProxy/httpconnect"
)

var httpConnectListenAddr string

var httpConnectCmd = &cobra.Command{
	Use:   "http-connect",
	Short: "Start HTTP CONNECT tunnel proxy",
	RunE: func(cmd *cobra.Command, args []string) error {
		conf, err := config.LoadHttpConnectConfig()
		if err != nil {
			return err
		}

		providerConfig, err := config.LoadProviderConfig(config.ProviderConfigPath)
		if err != nil {
			return err
		}
		secretKey := providerConfig.GetSecretKey()

		records := conf.GetAllRecordsWithInfo()
		if len(records) == 0 {
			return errors.New("no deployed http-connect functions found")
		}

		host, portStr, err := net.SplitHostPort(httpConnectListenAddr)
		if err != nil {
			return err
		}
		basePort, _ := strconv.Atoi(portStr)

		type PortMapping struct {
			Port     int
			Provider string
			Region   string
		}
		portMappings := []PortMapping{}

		for _, record := range records {
			port := record.Port
			if port == 0 {
				port = basePort + len(portMappings)
			}
			portMappings = append(portMappings, PortMapping{Port: port, Provider: record.Provider, Region: record.Region})

			go func(port int, apiURL, provider, region string) {
				opts := &httpconnect.Options{
					ListenAddr: fmt.Sprintf("%s:%d", host, port),
					ApiUrl:     apiURL,
					SecretKey:  secretKey,
				}
				if err := httpconnect.ServeProxy(opts); err != nil {
					logrus.Errorf("Port %d (%s.%s) failed: %v", port, provider, region, err)
				}
			}(port, record.Url, record.Provider, record.Region)
		}

		logrus.Infof("Started %d HTTP CONNECT proxies:", len(portMappings))
		for _, mapping := range portMappings {
			logrus.Infof("  Port %d -> %s.%s", mapping.Port, mapping.Provider, mapping.Region)
		}

		select {}
	},
}

func init() {
	rootCmd.AddCommand(httpConnectCmd)
	httpConnectCmd.Flags().StringVarP(&httpConnectListenAddr, "listen", "l", "", "host:port of the HTTP CONNECT proxy (base port)")
	httpConnectCmd.MarkFlagRequired("listen")
}
