package probe

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"
)

type Result struct {
	Host          string
	Subject       string
	Issuer        string  // 中间证书 CN
	IssuerOrg     string  // 签发 CA 组织名
	SANs          []string
	SerialNumber  string
	NotBefore     time.Time
	NotAfter      time.Time
	IsWildcard    bool
	DaysRemaining int
}

// Probe performs a real TLS handshake against host:port and returns the
// certificate currently being served. This is what makes the "no false
// alarms on replaced certs" guarantee: we only ever look at the live cert.
func Probe(host string, port int) (*Result, error) {
	return ProbeTimeout(host, port, 10*time.Second)
}

func ProbeTimeout(host string, port int, timeout time.Duration) (*Result, error) {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp",
		net.JoinHostPort(host, fmt.Sprintf("%d", port)),
		&tls.Config{ServerName: host})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no peer certificate presented")
	}
	return parse(host, state.PeerCertificates[0]), nil
}

func parse(host string, cert *x509.Certificate) *Result {
	r := &Result{
		Host:          host,
		Subject:       cert.Subject.CommonName,
		Issuer:        cert.Issuer.CommonName,
		SANs:          append([]string{}, cert.DNSNames...),
		SerialNumber:  serialHex(cert.SerialNumber),
		NotBefore:     cert.NotBefore,
		NotAfter:      cert.NotAfter,
		IsWildcard:    isWildcard(cert),
		DaysRemaining: int(time.Until(cert.NotAfter).Hours() / 24),
	}
	if len(cert.Issuer.Organization) > 0 {
		r.IssuerOrg = cert.Issuer.Organization[0]
	}
	if r.Subject == "" {
		r.Subject = cert.Subject.String()
	}
	if r.Issuer == "" {
		r.Issuer = cert.Issuer.String()
	}
	return r
}

func serialHex(n *big.Int) string {
	if n == nil {
		return ""
	}
	b := n.Bytes()
	if len(b) == 0 {
		return "0"
	}
	// 与 openssl x509 -serial 输出一致：连续大写 hex
	return fmt.Sprintf("%X", b)
}

func isWildcard(cert *x509.Certificate) bool {
	if strings.HasPrefix(cert.Subject.CommonName, "*.") {
		return true
	}
	for _, s := range cert.DNSNames {
		if strings.HasPrefix(s, "*.") {
			return true
		}
	}
	return false
}

// HTTPResult 网站健康探测结果：StatusCode 和 Error 二选一非空
type HTTPResult struct {
	StatusCode int
	Error      string
}

// HTTPProbe 用 GET 探测 https://host:port/path，10s 超时，不跟随重定向（看原始状态码）
func HTTPProbe(host string, port int, urlPath string) *HTTPResult {
	return HTTPProbeTimeout(host, port, urlPath, 10*time.Second)
}

func HTTPProbeTimeout(host string, port int, urlPath string, timeout time.Duration) *HTTPResult {
	if urlPath == "" {
		urlPath = "/"
	}
	// 默认端口不拼到 URL：某些 nginx/WAF 对 "host:443" 形式的 Host header 会返回 403
	hostPart := host
	if port != 0 && port != 443 {
		hostPart = net.JoinHostPort(host, fmt.Sprintf("%d", port))
	}
	full := fmt.Sprintf("https://%s%s", hostPart, urlPath)
	client := &http.Client{
		Timeout: timeout,
		// 不跟随重定向，直接看首次响应
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(full)
	if err != nil {
		return &HTTPResult{Error: err.Error()}
	}
	defer resp.Body.Close()
	// 主动丢弃 body，避免连接被占着
	_, _ = io.CopyN(io.Discard, resp.Body, 1024)
	return &HTTPResult{StatusCode: resp.StatusCode}
}