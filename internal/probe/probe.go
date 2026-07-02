package probe

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"net"
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