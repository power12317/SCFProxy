package httpconnect

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sirupsen/logrus"
)

type Options struct {
	ListenAddr string
	ApiUrl     string
	SecretKey  string
}

func ServeProxy(opts *Options) error {
	if opts.ApiUrl == "" {
		return errors.New("api URL is required")
	}

	ln, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return err
	}
	defer ln.Close()

	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		os.Exit(0)
	}()

	fmt.Printf("HTTP CONNECT proxy listening at %s\n", opts.ListenAddr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleClient(conn, opts.ApiUrl, opts.SecretKey)
	}
}

func handleClient(client net.Conn, apiURL, secretKey string) {
	defer client.Close()

	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		logrus.Debugf("read proxy request failed: %v", err)
		return
	}
	defer req.Body.Close()

	if req.Method == http.MethodConnect {
		handleConnect(client, br, req, apiURL, secretKey)
		return
	}

	handlePlainHTTP(client, br, req, apiURL, secretKey)
}

func handleConnect(client net.Conn, br *bufio.Reader, req *http.Request, apiURL, secretKey string) {
	target, err := canonicalTarget(req.Host, "443")
	if err != nil {
		writeProxyError(client, http.StatusBadRequest, err.Error())
		return
	}

	tunnel, err := dialTunnel(apiURL, target, secretKey)
	if err != nil {
		writeProxyError(client, http.StatusBadGateway, err.Error())
		return
	}
	defer tunnel.Close()

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}

	copyBufferedThenTunnel(client, br, tunnel)
}

func handlePlainHTTP(client net.Conn, br *bufio.Reader, req *http.Request, apiURL, secretKey string) {
	defaultPort := "80"
	if req.URL != nil && strings.EqualFold(req.URL.Scheme, "https") {
		defaultPort = "443"
	}

	host := req.Host
	if req.URL != nil && req.URL.Host != "" {
		host = req.URL.Host
	}
	target, err := canonicalTarget(host, defaultPort)
	if err != nil {
		writeProxyError(client, http.StatusBadRequest, err.Error())
		return
	}

	tunnel, err := dialTunnel(apiURL, target, secretKey)
	if err != nil {
		writeProxyError(client, http.StatusBadGateway, err.Error())
		return
	}
	defer tunnel.Close()

	req.RequestURI = ""
	req.Header.Del("Proxy-Connection")
	if err := req.Write(tunnel); err != nil {
		writeProxyError(client, http.StatusBadGateway, err.Error())
		return
	}

	if br.Buffered() > 0 {
		if _, err := io.CopyN(tunnel, br, int64(br.Buffered())); err != nil {
			return
		}
	}
	_, _ = io.Copy(client, tunnel)
}

func copyBufferedThenTunnel(client net.Conn, br *bufio.Reader, tunnel net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		if br.Buffered() > 0 {
			_, _ = io.CopyN(tunnel, br, int64(br.Buffered()))
		}
		_, _ = io.Copy(tunnel, client)
		_ = tunnel.Close()
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, tunnel)
		_ = client.Close()
		done <- struct{}{}
	}()
	<-done
}

func writeProxyError(conn net.Conn, code int, message string) {
	status := http.StatusText(code)
	if status == "" {
		status = "Proxy Error"
	}
	body := message + "\n"
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", code, status, len(body), body)
}
