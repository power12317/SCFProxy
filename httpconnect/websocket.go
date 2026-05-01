package httpconnect

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	websocketGUID   = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxFramePayload = 256 * 1024
)

type wsConn struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
	buf    []byte
}

func dialTunnel(rawURL, target, secretKey string) (net.Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	if u.Scheme != "wss" && u.Scheme != "ws" {
		return nil, fmt.Errorf("unsupported function url scheme: %s", u.Scheme)
	}

	q := u.Query()
	q.Set("target", target)
	u.RawQuery = q.Encode()
	if u.Path == "" {
		u.Path = "/tunnel"
	}

	conn, err := dialWSNetConn(u)
	if err != nil {
		return nil, err
	}

	key, err := newWebSocketKey()
	if err != nil {
		conn.Close()
		return nil, err
	}

	path := u.RequestURI()
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n", path, u.Host, key)
	if secretKey != "" {
		req += fmt.Sprintf("X-SCF-Secret-Key: %s\r\n", secretKey)
	}
	req += "\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s", resp.Status)
	}
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), websocketAccept(key); got != want {
		conn.Close()
		return nil, fmt.Errorf("invalid websocket accept")
	}

	return &wsConn{conn: conn, reader: br}, nil
}

func dialWSNetConn(u *url.URL) (net.Conn, error) {
	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		if u.Scheme == "wss" {
			host = net.JoinHostPort(host, "443")
		} else {
			host = net.JoinHostPort(host, "80")
		}
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if u.Scheme == "wss" {
		return tls.DialWithDialer(dialer, "tcp", host, &tls.Config{ServerName: u.Hostname()})
	}
	return dialer.Dial("tcp", host)
}

func newWebSocketKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (c *wsConn) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		opcode, payload, err := readWSFrame(c.reader)
		if err != nil {
			return 0, err
		}
		switch opcode {
		case 1, 2:
			c.buf = payload
		case 8:
			return 0, io.EOF
		case 9:
			_ = c.writeFrame(10, payload)
		}
	}

	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *wsConn) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > maxFramePayload {
			n = maxFramePayload
		}
		if err := c.writeFrame(2, p[:n]); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

func (c *wsConn) Close() error {
	_ = c.writeFrame(8, []byte{})
	return c.conn.Close()
}

func (c *wsConn) LocalAddr() net.Addr               { return c.conn.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr              { return c.conn.RemoteAddr() }
func (c *wsConn) SetDeadline(t time.Time) error     { return c.conn.SetDeadline(t) }
func (c *wsConn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return writeMaskedWSFrame(c.conn, opcode, payload)
}

func readWSFrame(r *bufio.Reader) (byte, []byte, error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	second, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}

	opcode := first & 0x0f
	masked := second&0x80 != 0
	length := uint64(second & 0x7f)
	switch length {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > maxFramePayload {
		return 0, nil, fmt.Errorf("websocket frame too large")
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

func writeMaskedWSFrame(w io.Writer, opcode byte, payload []byte) error {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 0xffff:
		header = append(header, 0x80|126, byte(length>>8), byte(length))
	default:
		header = append(header, 0x80|127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(length))
		header = append(header, b[:]...)
	}
	header = append(header, mask[:]...)
	if _, err := w.Write(header); err != nil {
		return err
	}

	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	_, err := w.Write(masked)
	return err
}

func canonicalTarget(host string, defaultPort string) (string, error) {
	if host == "" {
		return "", fmt.Errorf("missing host")
	}
	if strings.Contains(host, "://") {
		u, err := url.Parse(host)
		if err != nil {
			return "", err
		}
		host = u.Host
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host, nil
	}
	return net.JoinHostPort(host, defaultPort), nil
}
