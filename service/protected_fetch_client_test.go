package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

type staticSSRFResolver map[string][]net.IPAddr

func (r staticSSRFResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ips, ok := r[host]; ok {
		return ips, nil
	}
	return nil, fmt.Errorf("unexpected lookup for %s", host)
}

type ssrfResolverFunc func(context.Context, string) ([]net.IPAddr, error)

func (f ssrfResolverFunc) LookupIPAddr(
	ctx context.Context,
	host string,
) ([]net.IPAddr, error) {
	return f(ctx, host)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return f(request)
}

func staticProtection(protection *common.SSRFProtection) func() (*common.SSRFProtection, bool, error) {
	return func() (*common.SSRFProtection, bool, error) {
		return protection, true, nil
	}
}

func testConn(t *testing.T) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})
	return clientConn
}

func configureSSRFTestFetchSetting(t *testing.T) {
	t.Helper()
	original, err := config.ConfigToMap(system_setting.GetFetchSetting())
	require.NoError(t, err)
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("fetch_setting", original)
		require.NoError(t, updateErr)
		require.True(t, updated)
	})
	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection":     "true",
		"allow_private_ip":           "false",
		"domain_filter_mode":         "false",
		"ip_filter_mode":             "false",
		"domain_list":                "null",
		"ip_list":                    "null",
		"allowed_ports":              `["80","443"]`,
		"apply_ip_filter_for_domain": "true",
	})
	require.NoError(t, err)
	require.True(t, updated)
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	require.NoError(t, err)
	return parsedURL
}

type socksTarget struct {
	addressType byte
	host        string
	port        int
}

func startRecordingSOCKS5Proxy(t *testing.T) (string, <-chan socksTarget) {
	return startRecordingSOCKS5ProxyWithResponses(
		t,
		"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok",
	)
}

func startRecordingSOCKS5ProxyWithResponses(
	t *testing.T,
	responses ...string,
) (string, <-chan socksTarget) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, listener.Close())
	})

	targets := make(chan socksTarget, len(responses))
	go func() {
		for _, response := range responses {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			greeting := make([]byte, 2)
			if _, readErr := io.ReadFull(connection, greeting); readErr != nil {
				connection.Close()
				return
			}
			methods := make([]byte, int(greeting[1]))
			if _, readErr := io.ReadFull(connection, methods); readErr != nil {
				connection.Close()
				return
			}
			if _, writeErr := connection.Write([]byte{5, 0}); writeErr != nil {
				connection.Close()
				return
			}

			requestHeader := make([]byte, 4)
			if _, readErr := io.ReadFull(connection, requestHeader); readErr != nil {
				connection.Close()
				return
			}
			target := socksTarget{addressType: requestHeader[3]}
			switch requestHeader[3] {
			case 1:
				address := make([]byte, net.IPv4len)
				if _, readErr := io.ReadFull(connection, address); readErr != nil {
					connection.Close()
					return
				}
				target.host = net.IP(address).String()
			case 4:
				address := make([]byte, net.IPv6len)
				if _, readErr := io.ReadFull(connection, address); readErr != nil {
					connection.Close()
					return
				}
				target.host = net.IP(address).String()
			case 3:
				length := make([]byte, 1)
				if _, readErr := io.ReadFull(connection, length); readErr != nil {
					connection.Close()
					return
				}
				address := make([]byte, int(length[0]))
				if _, readErr := io.ReadFull(connection, address); readErr != nil {
					connection.Close()
					return
				}
				target.host = string(address)
			default:
				connection.Close()
				return
			}
			portBytes := make([]byte, 2)
			if _, readErr := io.ReadFull(connection, portBytes); readErr != nil {
				connection.Close()
				return
			}
			target.port = int(portBytes[0])<<8 | int(portBytes[1])
			targets <- target

			if _, writeErr := connection.Write([]byte{
				5, 0, 0, 1,
				127, 0, 0, 1,
				0, 0,
			}); writeErr != nil {
				connection.Close()
				return
			}
			request, readErr := http.ReadRequest(bufio.NewReader(connection))
			if readErr != nil {
				connection.Close()
				return
			}
			request.Body.Close()
			_, _ = io.WriteString(connection, response)
			connection.Close()
		}
	}()

	return "socks5://" + listener.Addr().String(), targets
}

func newRecordingKeepAliveDialer(
	t *testing.T,
) (
	func(context.Context, string, string) (net.Conn, error),
	<-chan string,
	*atomic.Int32,
) {
	t.Helper()
	addresses := make(chan string, 4)
	var dialCount atomic.Int32
	dial := func(
		_ context.Context,
		_ string,
		address string,
	) (net.Conn, error) {
		dialCount.Add(1)
		addresses <- address
		clientConnection, serverConnection := net.Pipe()
		go func() {
			defer serverConnection.Close()
			reader := bufio.NewReader(serverConnection)
			for {
				request, err := http.ReadRequest(reader)
				if err != nil {
					return
				}
				request.Body.Close()
				if _, err = io.WriteString(
					serverConnection,
					"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok",
				); err != nil {
					return
				}
			}
		}()
		return clientConnection, nil
	}
	return dial, addresses, &dialCount
}

func requireHTTPBody(t *testing.T, response *http.Response, expected string) {
	t.Helper()
	require.NotNil(t, response)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, expected, string(body))
}

func requireDialAddress(
	t *testing.T,
	addresses <-chan string,
	expected string,
) {
	t.Helper()
	select {
	case address := <-addresses:
		require.Equal(t, expected, address)
	default:
		t.Fatalf("expected a new dial to %s", expected)
	}
}

func TestProtectedFetchDialerRejectsPrivateReboundAddress(t *testing.T) {
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("127.0.0.1")}},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			t.Fatalf("dialContext should not be called for blocked address %s", address)
			return nil, nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:80")

	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "private IP address not allowed")
}

func TestProtectedFetchDialerRejectsMixedResolvedIPs(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("10.0.0.1")},
				{IP: net.ParseIP("8.8.8.8")},
			},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")
	require.Error(t, err)
	require.Nil(t, conn)

	require.Empty(t, dialed)
	require.Contains(t, err.Error(), "private IP address not allowed")
}

func TestProtectedFetchDialerDialsWhenAllResolvedIPsAllowed(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"safe.example": {
				{IP: net.ParseIP("8.8.8.8")},
				{IP: net.ParseIP("1.1.1.1")},
			},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:443")
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, []string{"8.8.8.8:443"}, dialed)
}

func TestProtectedFetchDialerAllowsPrivateIPWhenWhitelisted(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{
			"internal.example": {{IP: net.ParseIP("10.1.2.3")}},
		},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         true,
			DomainFilterMode:       false,
			IpFilterMode:           true,
			IpList:                 []string{"10.0.0.0/8"},
			ApplyIPFilterForDomain: true,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "internal.example:80")
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, []string{"10.1.2.3:80"}, dialed)
}

func TestProtectedFetchDialerSkipsResolvedIPCheckWhenDisabled(t *testing.T) {
	var dialed []string
	dialer := &protectedFetchDialer{
		resolver: staticSSRFResolver{},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return testConn(t), nil
		},
		getProtection: staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: false,
		}),
	}

	conn, err := dialer.DialContext(context.Background(), "tcp", "safe.example:80")
	require.NoError(t, err)
	require.NotNil(t, conn)

	require.Equal(t, []string{"safe.example:80"}, dialed)
}

func TestGetSSRFProtectedHTTPClientFallsBackToDefaultClientWhenProtectionDisabled(t *testing.T) {
	originalFetchSetting, err := config.ConfigToMap(system_setting.GetFetchSetting())
	require.NoError(t, err)
	originalHTTPClient := httpClient
	originalProtectedClient := ssrfProtectedHTTPClient
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("fetch_setting", originalFetchSetting)
		require.NoError(t, updateErr)
		require.True(t, updated)
		httpClient = originalHTTPClient
		ssrfProtectedHTTPClient = originalProtectedClient
	})

	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection": "false",
	})
	require.NoError(t, err)
	require.True(t, updated)
	genericRequests := 0
	httpClient = &http.Client{
		Transport: roundTripperFunc(func(
			request *http.Request,
		) (*http.Response, error) {
			genericRequests++
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("generic")),
				Header:     make(http.Header),
				Request:    request,
			}, nil
		}),
	}
	ssrfProtectedHTTPClient = newProtectedFetchHTTPClient()

	response, err := GetSSRFProtectedHTTPClient().Get(
		"http://private.example/resource",
	)
	require.NoError(t, err)
	requireHTTPBody(t, response, "generic")
	require.Equal(t, 1, genericRequests)
}

func TestGetSSRFProtectedHTTPClientWithProxyRejectsForwardProxies(t *testing.T) {
	configureSSRFTestFetchSetting(t)

	for _, rawProxyURL := range []string{
		"http://proxy.example:3128",
		"https://proxy.example:3129",
	} {
		t.Run(mustParseURL(t, rawProxyURL).Scheme, func(t *testing.T) {
			client, err := GetSSRFProtectedHTTPClientWithProxy(rawProxyURL)

			require.Error(t, err)
			require.Nil(t, client)
			require.Contains(
				t,
				err.Error(),
				"forward proxy cannot pin a protected fetch destination",
			)
		})
	}
}

func TestGetSSRFProtectedHTTPClientWithProxyPreservesOptOutCompatibility(t *testing.T) {
	original, err := config.ConfigToMap(system_setting.GetFetchSetting())
	require.NoError(t, err)
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("fetch_setting", original)
		require.NoError(t, updateErr)
		require.True(t, updated)
	})
	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection": "false",
	})
	require.NoError(t, err)
	require.True(t, updated)

	proxiedHosts := make(chan string, 1)
	forwardProxy := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		request *http.Request,
	) {
		proxiedHosts <- request.Host
		_, _ = io.WriteString(w, "proxied")
	}))
	t.Cleanup(forwardProxy.Close)

	actual, err := GetSSRFProtectedHTTPClientWithProxy(forwardProxy.URL)
	require.NoError(t, err)
	response, err := actual.Get("http://upstream.example/resource")
	require.NoError(t, err)
	requireHTTPBody(t, response, "proxied")
	require.Equal(t, "upstream.example", <-proxiedHosts)
}

func TestRetainedProtectedProxyClientRejectsForwardProxyAfterEnable(t *testing.T) {
	original, err := config.ConfigToMap(system_setting.GetFetchSetting())
	require.NoError(t, err)
	ResetProxyClientCache()
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("fetch_setting", original)
		require.NoError(t, updateErr)
		require.True(t, updated)
		ResetProxyClientCache()
	})
	updated, err := config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection": "false",
	})
	require.NoError(t, err)
	require.True(t, updated)

	proxyRequests := make(chan string, 1)
	forwardProxy := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		request *http.Request,
	) {
		proxyRequests <- request.Host
		_, _ = io.WriteString(w, "proxied")
	}))
	t.Cleanup(forwardProxy.Close)
	client, err := GetSSRFProtectedHTTPClientWithProxy(forwardProxy.URL)
	require.NoError(t, err)

	updated, err = config.GlobalConfig.Update("fetch_setting", map[string]string{
		"enable_ssrf_protection":     "true",
		"allow_private_ip":           "false",
		"domain_filter_mode":         "false",
		"ip_filter_mode":             "false",
		"domain_list":                "null",
		"ip_list":                    "null",
		"allowed_ports":              `["80","443"]`,
		"apply_ip_filter_for_domain": "true",
	})
	require.NoError(t, err)
	require.True(t, updated)

	response, err := client.Get("http://safe.example/resource")
	require.Error(t, err)
	require.Nil(t, response)
	require.Contains(
		t,
		err.Error(),
		"forward proxy cannot pin a protected fetch destination",
	)
	select {
	case host := <-proxyRequests:
		t.Fatalf(
			"forward proxy received protected destination %s after enable",
			host,
		)
	default:
	}
}

func TestProtectedProxyClientCacheCanonicalizesAndInvalidates(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	first, err := GetSSRFProtectedHTTPClientWithProxy(
		"socks5://proxy.example",
	)
	require.NoError(t, err)
	alias, err := GetSSRFProtectedHTTPClientWithProxy(
		"socks5://proxy.example:1080/",
	)
	require.NoError(t, err)
	require.Same(t, first, alias)

	InvalidateProxyClient("socks5://proxy.example")
	afterInvalidation, err := GetSSRFProtectedHTTPClientWithProxy(
		"socks5://proxy.example",
	)
	require.NoError(t, err)
	require.NotSame(t, first, afterInvalidation)
}

func TestProtectedSOCKSProxyReceivesValidatedIPLiteral(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	rawProxyURL, targets := startRecordingSOCKS5Proxy(t)
	proxyURL := mustParseURL(t, rawProxyURL)
	client, err := newProtectedFetchHTTPClientWithSOCKSProxy(
		proxyURL,
		staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("8.8.8.8")}},
		},
		staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
			ApplyIPFilterForDomain: true,
		}),
	)
	require.NoError(t, err)
	t.Cleanup(client.CloseIdleConnections)

	response, err := client.Get("http://safe.example/resource")
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "ok", string(body))
	target := <-targets
	require.Equal(t, byte(1), target.addressType)
	require.Equal(t, "8.8.8.8", target.host)
	require.Equal(t, 80, target.port)
}

func TestProtectedSOCKSProxyRejectsReboundAddressBeforeConnect(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	rawProxyURL, targets := startRecordingSOCKS5Proxy(t)
	client, err := newProtectedFetchHTTPClientWithSOCKSProxy(
		mustParseURL(t, rawProxyURL),
		staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("127.0.0.1")}},
		},
		staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
			ApplyIPFilterForDomain: true,
		}),
	)
	require.NoError(t, err)
	t.Cleanup(client.CloseIdleConnections)

	response, err := client.Get("http://safe.example/resource")

	require.Error(t, err)
	require.Nil(t, response)
	require.Contains(t, err.Error(), "private IP address not allowed")
	select {
	case target := <-targets:
		t.Fatalf(
			"SOCKS proxy received blocked target %s:%s",
			target.host,
			strconv.Itoa(target.port),
		)
	default:
	}
}

func TestProtectedSOCKSProxyRedirectsFailClosed(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	redirectResponse := "HTTP/1.1 302 Found\r\n" +
		"Location: http://127.0.0.1/private\r\n" +
		"Content-Length: 0\r\nConnection: close\r\n\r\n"
	rawProxyURL, targets := startRecordingSOCKS5ProxyWithResponses(
		t,
		redirectResponse,
		"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok",
	)
	client, err := newProtectedFetchHTTPClientWithSOCKSProxy(
		mustParseURL(t, rawProxyURL),
		staticSSRFResolver{},
		staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
			ApplyIPFilterForDomain: true,
		}),
	)
	require.NoError(t, err)
	t.Cleanup(client.CloseIdleConnections)

	response, err := client.Get("http://8.8.8.8/start")

	require.Error(t, err)
	require.NotNil(t, response)
	require.Equal(t, http.StatusFound, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.Contains(t, err.Error(), "private IP address not allowed")
	firstTarget := <-targets
	require.Equal(t, byte(1), firstTarget.addressType)
	require.Equal(t, "8.8.8.8", firstTarget.host)
	select {
	case target := <-targets:
		t.Fatalf("blocked redirect reached SOCKS target %s", target.host)
	default:
	}
}

func TestProtectedSOCKSProxyRedirectRechecksDNSAtDialTime(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	redirectResponse := "HTTP/1.1 302 Found\r\n" +
		"Location: http://rebound.example/private\r\n" +
		"Content-Length: 0\r\nConnection: close\r\n\r\n"
	rawProxyURL, targets := startRecordingSOCKS5ProxyWithResponses(
		t,
		redirectResponse,
		"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok",
	)
	client, err := newProtectedFetchHTTPClientWithSOCKSProxy(
		mustParseURL(t, rawProxyURL),
		staticSSRFResolver{
			"start.example":   {{IP: net.ParseIP("8.8.8.8")}},
			"rebound.example": {{IP: net.ParseIP("127.0.0.1")}},
		},
		staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
			ApplyIPFilterForDomain: true,
		}),
	)
	require.NoError(t, err)
	t.Cleanup(client.CloseIdleConnections)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return nil
	}

	response, err := client.Get("http://start.example/start")

	require.Error(t, err)
	require.Nil(t, response)
	require.Contains(t, err.Error(), "private IP address not allowed")
	firstTarget := <-targets
	require.Equal(t, byte(1), firstTarget.addressType)
	require.Equal(t, "8.8.8.8", firstTarget.host)
	select {
	case target := <-targets:
		t.Fatalf("rebound redirect reached SOCKS target %s", target.host)
	default:
	}
}

func TestProtectedFetchClientRetiresKeepAliveWhenDomainIPFilteringEnables(
	t *testing.T,
) {
	var current atomic.Pointer[common.SSRFProtection]
	current.Store(&common.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
		ApplyIPFilterForDomain: false,
	})
	dial, addresses, dialCount := newRecordingKeepAliveDialer(t)
	client := newProtectedFetchHTTPClientWithDialer(
		staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("8.8.8.8")}},
		},
		dial,
		func() (*common.SSRFProtection, bool, error) {
			return current.Load(), true, nil
		},
	)
	t.Cleanup(client.CloseIdleConnections)

	response, err := client.Get("http://safe.example/before")
	require.NoError(t, err)
	requireHTTPBody(t, response, "ok")
	requireDialAddress(t, addresses, "safe.example:80")

	current.Store(&common.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
		ApplyIPFilterForDomain: true,
	})
	response, err = client.Get("http://safe.example/after")
	require.NoError(t, err)
	requireHTTPBody(t, response, "ok")

	require.Equal(t, int32(2), dialCount.Load())
	requireDialAddress(t, addresses, "8.8.8.8:80")
}

func TestProtectedFetchClientRetiresKeepAliveWhenPrivateIPAccessRevokes(
	t *testing.T,
) {
	var current atomic.Pointer[common.SSRFProtection]
	current.Store(&common.SSRFProtection{
		AllowPrivateIp:         true,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
		ApplyIPFilterForDomain: true,
	})
	resolver := ssrfResolverFunc(func(
		_ context.Context,
		host string,
	) ([]net.IPAddr, error) {
		if host != "switch.example" {
			return nil, fmt.Errorf("unexpected lookup for %s", host)
		}
		if current.Load().AllowPrivateIp {
			return []net.IPAddr{{IP: net.ParseIP("10.0.0.7")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	})
	dial, addresses, dialCount := newRecordingKeepAliveDialer(t)
	client := newProtectedFetchHTTPClientWithDialer(
		resolver,
		dial,
		func() (*common.SSRFProtection, bool, error) {
			return current.Load(), true, nil
		},
	)
	t.Cleanup(client.CloseIdleConnections)

	response, err := client.Get("http://switch.example/before")
	require.NoError(t, err)
	requireHTTPBody(t, response, "ok")
	requireDialAddress(t, addresses, "10.0.0.7:80")

	current.Store(&common.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
		ApplyIPFilterForDomain: true,
	})
	response, err = client.Get("http://switch.example/after")
	require.NoError(t, err)
	requireHTTPBody(t, response, "ok")

	require.Equal(t, int32(2), dialCount.Load())
	requireDialAddress(t, addresses, "8.8.8.8:80")
}

func TestProtectedFetchClientRetiresKeepAliveWhenIPListRevokesAddress(
	t *testing.T,
) {
	var current atomic.Pointer[common.SSRFProtection]
	current.Store(&common.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
		ApplyIPFilterForDomain: true,
	})
	dial, addresses, dialCount := newRecordingKeepAliveDialer(t)
	client := newProtectedFetchHTTPClientWithDialer(
		staticSSRFResolver{
			"listed.example": {{IP: net.ParseIP("8.8.8.8")}},
		},
		dial,
		func() (*common.SSRFProtection, bool, error) {
			return current.Load(), true, nil
		},
	)
	t.Cleanup(client.CloseIdleConnections)

	response, err := client.Get("http://listed.example/before")
	require.NoError(t, err)
	requireHTTPBody(t, response, "ok")
	requireDialAddress(t, addresses, "8.8.8.8:80")

	current.Store(&common.SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		IpList:                 []string{"8.8.8.8"},
		AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
		ApplyIPFilterForDomain: true,
	})
	response, err = client.Get("http://listed.example/after")

	require.Error(t, err)
	require.Nil(t, response)
	require.Contains(t, err.Error(), "ip in blacklist")
	require.Equal(t, int32(1), dialCount.Load())
	select {
	case address := <-addresses:
		t.Fatalf("revoked address reached connector: %s", address)
	default:
	}
}

func TestProtectedFetchClientPinsAllowedDestinationAtDialTime(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	var dialed []string
	client := newProtectedFetchHTTPClientWithDialer(
		staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("8.8.8.8")}},
		},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("stop after pinned dial")
		},
		staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	)
	req, err := http.NewRequest(http.MethodGet, "http://safe.example/resource", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Equal(t, []string{"8.8.8.8:80"}, dialed)
}

func TestProtectedFetchClientRejectsPrivateTargetBeforeDial(t *testing.T) {
	configureSSRFTestFetchSetting(t)
	var dialed []string
	client := newProtectedFetchHTTPClientWithDialer(
		staticSSRFResolver{},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed = append(dialed, address)
			return nil, errors.New("target should not be dialed")
		},
		staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			ApplyIPFilterForDomain: true,
		}),
	)
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/resource", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.Error(t, err)
	require.Nil(t, resp)
	require.Contains(t, err.Error(), "private IP address not allowed")
	require.Empty(t, dialed)
}

func TestProtectedFetchClientReusesConnectionWhenPolicyIsUnchanged(t *testing.T) {
	dial, addresses, dialCount := newRecordingKeepAliveDialer(t)
	client := newProtectedFetchHTTPClientWithDialer(
		staticSSRFResolver{
			"safe.example": {{IP: net.ParseIP("8.8.8.8")}},
		},
		dial,
		staticProtection(&common.SSRFProtection{
			AllowPrivateIp:         false,
			DomainFilterMode:       false,
			IpFilterMode:           false,
			AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
			ApplyIPFilterForDomain: true,
		}),
	)
	t.Cleanup(client.CloseIdleConnections)

	for _, path := range []string{"/first", "/second"} {
		response, err := client.Get("http://safe.example" + path)
		require.NoError(t, err)
		requireHTTPBody(t, response, "ok")
	}

	require.Equal(t, int32(1), dialCount.Load())
	requireDialAddress(t, addresses, "8.8.8.8:80")
}

func TestProtectedFetchClientIgnoresEnvironmentProxy(t *testing.T) {
	const helperEnvironment = "NA_ISSUE_0032_ENVIRONMENT_PROXY_HELPER"
	if os.Getenv(helperEnvironment) == "1" {
		dial, addresses, _ := newRecordingKeepAliveDialer(t)
		client := newProtectedFetchHTTPClientWithDialer(
			staticSSRFResolver{
				"safe.example": {{IP: net.ParseIP("8.8.8.8")}},
			},
			dial,
			staticProtection(&common.SSRFProtection{
				AllowPrivateIp:         false,
				DomainFilterMode:       false,
				IpFilterMode:           false,
				AllowedPorts:           []common.PortRange{{Start: 80, End: 80}},
				ApplyIPFilterForDomain: true,
			}),
		)
		t.Cleanup(client.CloseIdleConnections)

		response, err := client.Get("http://safe.example/resource")
		require.NoError(t, err)
		requireHTTPBody(t, response, "ok")
		requireDialAddress(t, addresses, "8.8.8.8:80")
		return
	}

	environment := make([]string, 0, len(os.Environ())+5)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(
		environment,
		helperEnvironment+"=1",
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
		"ALL_PROXY=http://127.0.0.1:1",
		"NO_PROXY=",
	)
	command := exec.Command(
		os.Args[0],
		"-test.run=^TestProtectedFetchClientIgnoresEnvironmentProxy$",
	)
	command.Env = environment
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}
