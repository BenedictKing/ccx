package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

const localTLSCertValidity = 365 * 24 * time.Hour
const defaultProtocolDetectionTimeout = 10 * time.Second

type serverEndpoint struct {
	Scheme string
	Host   string
}

func endpointForEnv(envCfg *config.EnvConfig) serverEndpoint {
	scheme := "http"
	if envCfg.EnableHTTPS {
		scheme = "https"
	}
	return serverEndpoint{
		Scheme: scheme,
		Host:   endpointHostForEnv(envCfg),
	}
}

func (e serverEndpoint) URL(path string) string {
	return fmt.Sprintf("%s://%s%s", e.Scheme, e.Host, path)
}

func endpointHostForEnv(envCfg *config.EnvConfig) string {
	host := strings.TrimSpace(envCfg.BindHost)
	switch host {
	case "", "0.0.0.0", "::":
		host = "localhost"
	}
	return net.JoinHostPort(host, strconv.Itoa(envCfg.Port))
}

func listenAddressForEnv(envCfg *config.EnvConfig) string {
	host := strings.TrimSpace(envCfg.BindHost)
	if host == "" {
		return fmt.Sprintf(":%d", envCfg.Port)
	}
	return net.JoinHostPort(host, strconv.Itoa(envCfg.Port))
}

func configureServerTLS(srv *http.Server, envCfg *config.EnvConfig) error {
	if !envCfg.EnableHTTPS {
		return nil
	}
	srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if envCfg.TLSCertFile != "" || envCfg.TLSKeyFile != "" {
		if envCfg.TLSCertFile == "" || envCfg.TLSKeyFile == "" {
			return fmt.Errorf("TLS_CERT_FILE 和 TLS_KEY_FILE 必须同时设置")
		}
		cert, err := tls.LoadX509KeyPair(envCfg.TLSCertFile, envCfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("加载 HTTPS 证书失败: %w%s", err, tlsPathHint(envCfg.TLSCertFile, envCfg.TLSKeyFile))
		}
		srv.TLSConfig.Certificates = []tls.Certificate{cert}
		return nil
	}
	if !envCfg.TLSAutoCert {
		return fmt.Errorf("ENABLE_HTTPS=true requires TLS_CERT_FILE/TLS_KEY_FILE, or TLS_AUTO_CERT=true for local self-signed TLS")
	}
	cert, err := generateLocalhostCertificate(time.Now())
	if err != nil {
		return fmt.Errorf("生成本地 HTTPS 自签名证书失败: %w", err)
	}
	srv.TLSConfig.Certificates = []tls.Certificate{cert}
	return nil
}

func ensureDefaultNextProtos(tlsConfig *tls.Config) {
	if !containsString(tlsConfig.NextProtos, "http/1.1") {
		tlsConfig.NextProtos = append(tlsConfig.NextProtos, "http/1.1")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func startHTTPServer(srv *http.Server, envCfg *config.EnvConfig) error {
	if !envCfg.EnableHTTPS {
		return srv.ListenAndServe()
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	return serveHTTPAndHTTPS(srv, ln)
}

func serveHTTPAndHTTPS(srv *http.Server, ln net.Listener) error {
	if srv.TLSConfig == nil {
		return fmt.Errorf("HTTPS 已启用但 TLSConfig 未初始化")
	}
	ensureDefaultNextProtos(srv.TLSConfig)
	timeout := srv.ReadHeaderTimeout
	if timeout <= 0 {
		timeout = srv.ReadTimeout
	}
	if timeout <= 0 {
		timeout = defaultProtocolDetectionTimeout
	}
	return srv.Serve(newProtocolDetectingListener(ln, srv.TLSConfig, timeout))
}

func tlsPathHint(certFile, keyFile string) string {
	if filepath.IsAbs(certFile) && filepath.IsAbs(keyFile) {
		return ""
	}
	return "；TLS_CERT_FILE/TLS_KEY_FILE 建议使用展开后的绝对路径，相对路径会按 CCX 进程工作目录解析"
}

type protocolDetectingListener struct {
	net.Listener
	tlsConfig *tls.Config
	timeout   time.Duration
	conns     chan acceptResult
	done      chan struct{}
	closeOnce sync.Once
}

type acceptResult struct {
	conn net.Conn
	err  error
}

func newProtocolDetectingListener(ln net.Listener, tlsConfig *tls.Config, timeout time.Duration) *protocolDetectingListener {
	listener := &protocolDetectingListener{
		Listener:  ln,
		tlsConfig: tlsConfig,
		timeout:   timeout,
		conns:     make(chan acceptResult, 64),
		done:      make(chan struct{}),
	}
	go listener.acceptLoop()
	return listener
}

func (l *protocolDetectingListener) Accept() (net.Conn, error) {
	select {
	case result := <-l.conns:
		return result.conn, result.err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *protocolDetectingListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.done)
	})
	return l.Listener.Close()
}

func (l *protocolDetectingListener) acceptLoop() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			l.sendResult(acceptResult{err: err})
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			}
			return
		}
		go l.detect(conn)
	}
}

func (l *protocolDetectingListener) detect(conn net.Conn) {
	detected, err := detectProtocol(conn, l.tlsConfig, l.timeout)
	if err != nil {
		_ = conn.Close()
		return
	}
	l.sendResult(acceptResult{conn: detected})
}

func (l *protocolDetectingListener) sendResult(result acceptResult) {
	select {
	case l.conns <- result:
	case <-l.done:
		if result.conn != nil {
			_ = result.conn.Close()
		}
	}
}

func detectProtocol(conn net.Conn, tlsConfig *tls.Config, timeout time.Duration) (net.Conn, error) {
	if timeout > 0 {
		if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return nil, err
		}
		defer conn.SetReadDeadline(time.Time{})
	}
	firstByte := make([]byte, 1)
	n, err := conn.Read(firstByte)
	if err != nil {
		return nil, err
	}
	buffered := &prefixedConn{Conn: conn, prefix: firstByte[:n]}
	if n == 1 && firstByte[0] == 0x16 {
		return tls.Server(buffered, tlsConfig.Clone()), nil
	}
	return buffered, nil
}

type prefixedConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixedConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func generateLocalhostCertificate(now time.Time) (tls.Certificate, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, err
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(localTLSCertValidity),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return tls.X509KeyPair(certPEM, keyPEM)
}
