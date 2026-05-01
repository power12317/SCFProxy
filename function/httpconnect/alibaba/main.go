package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	listenAddr       = "0.0.0.0:9000"
	authHeader       = "X-SCF-Secret-Key"
	secretEnvKey     = "SCF_SECRET_KEY"
	websocketGUID    = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxFramePayload  = 256 * 1024
	remoteBufferSize = 32 * 1024
)

func main() {
	http.HandleFunc("/tunnel", handleTunnel)
	http.HandleFunc("/", handleTunnel)
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		fmt.Println(err)
	}
}

func handleTunnel(w http.ResponseWriter, r *http.Request) {
	expectedSecret := os.Getenv(secretEnvKey)
	if expectedSecret != "" && r.Header.Get(authHeader) != expectedSecret {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	target := r.URL.Query().Get("target")
	if target == "" {
		http.Error(w, "missing target", http.StatusBadRequest)
		return
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		http.Error(w, "invalid target", http.StatusBadRequest)
		return
	}

	clientConn, brw, err := upgradeWebSocket(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer clientConn.Close()

	remoteConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		writeWSFrame(clientConn, 8, []byte{})
		return
	}
	defer remoteConn.Close()

	clientWriter := &lockedWriter{w: clientConn}
	done := make(chan struct{}, 2)
	go func() {
		defer remoteConn.Close()
		copyWebSocketToTCP(brw.Reader, remoteConn, clientWriter)
		done <- struct{}{}
	}()
	go func() {
		defer clientConn.Close()
		copyTCPToWebSocket(remoteConn, clientWriter)
		done <- struct{}{}
	}()
	<-done
}

func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, nil, fmt.Errorf("missing websocket upgrade")
	}
	if !headerContains(r.Header.Get("Connection"), "upgrade") {
		return nil, nil, fmt.Errorf("missing upgrade connection")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, nil, fmt.Errorf("missing websocket key")
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacking not supported")
	}
	conn, brw, err := hijacker.Hijack()
	if err != nil {
		return nil, nil, err
	}

	accept := websocketAccept(key)
	fmt.Fprintf(brw, "HTTP/1.1 101 Switching Protocols\r\n")
	fmt.Fprintf(brw, "Upgrade: websocket\r\n")
	fmt.Fprintf(brw, "Connection: Upgrade\r\n")
	fmt.Fprintf(brw, "Sec-WebSocket-Accept: %s\r\n\r\n", accept)
	if err := brw.Flush(); err != nil {
		conn.Close()
		return nil, nil, err
	}

	return conn, brw, nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func headerContains(value, token string) bool {
	token = strings.ToLower(token)
	for _, item := range strings.Split(value, ",") {
		if strings.ToLower(strings.TrimSpace(item)) == token {
			return true
		}
	}
	return false
}

func copyWebSocketToTCP(r *bufio.Reader, dst net.Conn, clientWriter io.Writer) {
	for {
		opcode, payload, err := readWSFrame(r, true)
		if err != nil {
			return
		}
		switch opcode {
		case 1, 2:
			if len(payload) > 0 {
				if _, err := dst.Write(payload); err != nil {
					return
				}
			}
		case 8:
			return
		case 9:
			_ = writeWSFrame(clientWriter, 10, payload)
		}
	}
}

func copyTCPToWebSocket(src net.Conn, dst io.Writer) {
	buf := make([]byte, remoteBufferSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if err := writeWSFrame(dst, 2, buf[:n]); err != nil {
				return
			}
		}
		if err != nil {
			_ = writeWSFrame(dst, 8, []byte{})
			return
		}
	}
}

func readWSFrame(r *bufio.Reader, expectMasked bool) (byte, []byte, error) {
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
	if expectMasked && !masked {
		return 0, nil, fmt.Errorf("expected masked frame")
	}

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
		return 0, nil, fmt.Errorf("frame too large")
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

func writeWSFrame(w io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, byte(length))
	case length <= 0xffff:
		header = append(header, 126, byte(length>>8), byte(length))
	default:
		header = append(header, 127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(length))
		header = append(header, b[:]...)
	}
	frame := append(header, payload...)
	_, err := w.Write(frame)
	return err
}

type lockedWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}
