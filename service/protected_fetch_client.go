package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"golang.org/x/net/proxy"
)

type ssrfResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type protectedFetchDialer struct {
	resolver      ssrfResolver
	dialContext   func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection func() (*common.SSRFProtection, bool, error)
}

type ssrfProtectedRoundTripper struct {
	resolver             ssrfResolver
	dialContext          func(ctx context.Context, network, address string) (net.Conn, error)
	getProtection        func() (*common.SSRFProtection, bool, error)
	unprotectedTransport http.RoundTripper
	rejectForwardProxy   bool

	mutex             sync.Mutex
	transport         *http.Transport
	policyFingerprint [sha256.Size]byte
	hasFingerprint    bool
}

type protectedFetchPolicyFingerprint struct {
	Enabled                bool
	AllowPrivateIP         bool
	DomainFilterMode       bool
	DomainList             []string
	IPFilterMode           bool
	IPList                 []string
	AllowedPorts           []common.PortRange
	ApplyIPFilterForDomain bool
}

func currentFetchProtection() (*common.SSRFProtection, bool, error) {
	fetchSetting := system_setting.GetFetchSetting()
	if !fetchSetting.EnableSSRFProtection {
		return nil, false, nil
	}

	protection, err := common.NewSSRFProtectionFromFetchSetting(
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	)
	if err != nil {
		return nil, true, err
	}
	return protection, true, nil
}

func newProtectedFetchHTTPClient() *http.Client {
	client := newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
	roundTripper := client.Transport.(*ssrfProtectedRoundTripper)
	roundTripper.unprotectedTransport = clientTransport(GetHttpClient())
	return client
}

func newProtectedFetchHTTPClientWithDialer(resolver ssrfResolver, dialContext func(ctx context.Context, network, address string) (net.Conn, error), getProtection func() (*common.SSRFProtection, bool, error)) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	if dialContext == nil {
		netDialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		dialContext = netDialer.DialContext
	}
	if getProtection == nil {
		getProtection = currentFetchProtection
	}

	client := &http.Client{
		Transport: &ssrfProtectedRoundTripper{
			resolver:      resolver,
			dialContext:   dialContext,
			getProtection: getProtection,
		},
		CheckRedirect: checkProtectedFetchRedirect,
		Timeout:       relayRequestTimeout(),
	}
	return client
}

func clientTransport(client *http.Client) http.RoundTripper {
	if client == nil || client.Transport == nil {
		return http.DefaultTransport
	}
	return client.Transport
}

func newProtectedFetchHTTPClientWithSOCKSProxy(
	proxyURL *url.URL,
	resolver ssrfResolver,
	getProtection func() (*common.SSRFProtection, bool, error),
) (*http.Client, error) {
	genericClient, err := newProxyHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	return newProtectedFetchHTTPClientWithSOCKSProxyAndFallback(
		proxyURL,
		resolver,
		getProtection,
		clientTransport(genericClient),
	)
}

func newProtectedFetchHTTPClientWithSOCKSProxyAndFallback(
	proxyURL *url.URL,
	resolver ssrfResolver,
	getProtection func() (*common.SSRFProtection, bool, error),
	unprotectedTransport http.RoundTripper,
) (*http.Client, error) {
	if proxyURL == nil ||
		(proxyURL.Scheme != "socks5" && proxyURL.Scheme != "socks5h") {
		return nil, fmt.Errorf("protected fetch proxy must use socks5 or socks5h")
	}
	forwardDialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	socksDialer, err := proxy.FromURL(proxyURL, forwardDialer)
	if err != nil {
		return nil, err
	}
	contextDialer, ok := socksDialer.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("SOCKS proxy dialer does not support context cancellation")
	}
	client := newProtectedFetchHTTPClientWithDialer(
		resolver,
		contextDialer.DialContext,
		getProtection,
	)
	roundTripper := client.Transport.(*ssrfProtectedRoundTripper)
	roundTripper.unprotectedTransport = unprotectedTransport
	return client, nil
}

func newProtectedFetchHTTPClientWithForwardProxyFallback(
	unprotectedTransport http.RoundTripper,
) *http.Client {
	client := newProtectedFetchHTTPClientWithDialer(nil, nil, nil)
	roundTripper := client.Transport.(*ssrfProtectedRoundTripper)
	roundTripper.unprotectedTransport = unprotectedTransport
	roundTripper.rejectForwardProxy = true
	return client
}

func validateProtectedFetchTargetURL(
	targetURL *url.URL,
	getProtection func() (*common.SSRFProtection, bool, error),
) error {
	if targetURL == nil {
		return fmt.Errorf("invalid request URL")
	}
	if targetURL.Scheme != "http" && targetURL.Scheme != "https" {
		return fmt.Errorf(
			"unsupported protocol: %s (only http/https allowed)",
			targetURL.Scheme,
		)
	}
	protection, enabled, err := getProtection()
	if err != nil || !enabled {
		return err
	}
	host := targetURL.Hostname()
	portText := targetURL.Port()
	if portText == "" {
		if targetURL.Scheme == "https" {
			portText = "443"
		} else {
			portText = "80"
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("invalid port: %s", portText)
	}
	return protection.ValidateNetworkTarget(host, port)
}

func protectedFetchPolicySnapshot(
	getProtection func() (*common.SSRFProtection, bool, error),
) (
	*common.SSRFProtection,
	bool,
	[sha256.Size]byte,
	error,
) {
	protection, enabled, err := getProtection()
	if err != nil {
		return nil, enabled, [sha256.Size]byte{}, err
	}
	fingerprintSource := protectedFetchPolicyFingerprint{Enabled: enabled}
	if !enabled {
		payload, marshalErr := common.Marshal(fingerprintSource)
		if marshalErr != nil {
			return nil, false, [sha256.Size]byte{}, marshalErr
		}
		return nil, false, sha256.Sum256(payload), nil
	}
	if protection == nil {
		return nil, true, [sha256.Size]byte{}, fmt.Errorf(
			"SSRF protection is enabled without a policy",
		)
	}

	snapshot := *protection
	snapshot.DomainList = append([]string(nil), protection.DomainList...)
	snapshot.IpList = append([]string(nil), protection.IpList...)
	snapshot.AllowedPorts = append([]common.PortRange(nil), protection.AllowedPorts...)
	fingerprintSource.AllowPrivateIP = snapshot.AllowPrivateIp
	fingerprintSource.DomainFilterMode = snapshot.DomainFilterMode
	fingerprintSource.DomainList = snapshot.DomainList
	fingerprintSource.IPFilterMode = snapshot.IpFilterMode
	fingerprintSource.IPList = snapshot.IpList
	fingerprintSource.AllowedPorts = snapshot.AllowedPorts
	fingerprintSource.ApplyIPFilterForDomain =
		snapshot.ApplyIPFilterForDomain
	payload, err := common.Marshal(fingerprintSource)
	if err != nil {
		return nil, true, [sha256.Size]byte{}, err
	}
	return &snapshot, true, sha256.Sum256(payload), nil
}

func (t *ssrfProtectedRoundTripper) transportForPolicy(
	protection *common.SSRFProtection,
	fingerprint [sha256.Size]byte,
) *http.Transport {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.hasFingerprint && t.policyFingerprint == fingerprint &&
		t.transport != nil {
		return t.transport
	}
	if t.transport != nil {
		t.transport.CloseIdleConnections()
	}
	t.policyFingerprint = fingerprint
	t.hasFingerprint = true
	protectedDialer := &protectedFetchDialer{
		resolver:    t.resolver,
		dialContext: t.dialContext,
		getProtection: func() (*common.SSRFProtection, bool, error) {
			return protection, true, nil
		},
	}
	t.transport = &http.Transport{
		MaxIdleConns:        common.RelayMaxIdleConns,
		MaxIdleConnsPerHost: common.RelayMaxIdleConnsPerHost,
		IdleConnTimeout:     time.Duration(common.RelayIdleConnTimeout) * time.Second,
		ForceAttemptHTTP2:   true,
		Proxy:               nil,
		DialContext:         protectedDialer.DialContext,
	}
	if common.TLSInsecureSkipVerify {
		t.transport.TLSClientConfig = common.InsecureTLSConfig
	}
	return t.transport
}

func (t *ssrfProtectedRoundTripper) observeDisabledPolicy(
	fingerprint [sha256.Size]byte,
) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.hasFingerprint && t.policyFingerprint == fingerprint {
		return
	}
	if t.transport != nil {
		t.transport.CloseIdleConnections()
		t.transport = nil
	}
	t.policyFingerprint = fingerprint
	t.hasFingerprint = true
}

func (t *ssrfProtectedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("invalid request")
	}
	protection, enabled, fingerprint, err := protectedFetchPolicySnapshot(
		t.getProtection,
	)
	if err != nil {
		return nil, err
	}
	if !enabled {
		t.observeDisabledPolicy(fingerprint)
		unprotectedTransport := t.unprotectedTransport
		if unprotectedTransport == nil {
			unprotectedTransport = http.DefaultTransport
		}
		return unprotectedTransport.RoundTrip(req)
	}
	if t.rejectForwardProxy {
		return nil, fmt.Errorf(
			"forward proxy cannot pin a protected fetch destination; use socks5 or socks5h",
		)
	}
	if err := validateProtectedFetchTargetURL(
		req.URL,
		func() (*common.SSRFProtection, bool, error) {
			return protection, true, nil
		},
	); err != nil {
		return nil, err
	}
	return t.transportForPolicy(protection, fingerprint).RoundTrip(req)
}

func (t *ssrfProtectedRoundTripper) CloseIdleConnections() {
	t.mutex.Lock()
	transport := t.transport
	unprotectedTransport := t.unprotectedTransport
	t.mutex.Unlock()
	if transport != nil {
		transport.CloseIdleConnections()
	}
	if closer, ok := unprotectedTransport.(interface {
		CloseIdleConnections()
	}); ok {
		closer.CloseIdleConnections()
	}
}

func (d *protectedFetchDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	protection, enabled, err := d.getProtection()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return d.dialContext(ctx, network, addr)
	}

	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %s: %w", addr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %s", portText)
	}
	if err := protection.ValidateNetworkTarget(host, port); err != nil {
		return nil, err
	}

	if ip := net.ParseIP(host); ip != nil {
		return d.dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
	}
	if !protection.ApplyIPFilterForDomain {
		return d.dialContext(ctx, network, addr)
	}

	resolved, err := d.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS resolution failed for %s: %v", host, err)
	}

	var candidateIPs []net.IP
	for _, ipAddr := range resolved {
		ip := ipAddr.IP
		if ip == nil || !networkAllowsIP(network, ip) {
			continue
		}
		if err := protection.ValidateResolvedIP(host, ip); err != nil {
			return nil, err
		}
		candidateIPs = append(candidateIPs, ip)
	}

	var lastDialErr error
	for _, ip := range candidateIPs {
		conn, err := d.dialContext(ctx, network, net.JoinHostPort(ip.String(), portText))
		if err == nil {
			return conn, nil
		}
		lastDialErr = err
	}

	if lastDialErr != nil {
		return nil, lastDialErr
	}
	return nil, fmt.Errorf("DNS resolution for %s returned no usable IP addresses", host)
}

func networkAllowsIP(network string, ip net.IP) bool {
	switch network {
	case "tcp4":
		return ip.To4() != nil
	case "tcp6":
		return ip.To4() == nil
	default:
		return true
	}
}
