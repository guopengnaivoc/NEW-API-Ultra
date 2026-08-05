package common

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSRFProtectionRejectsLiteralPrivateAndReservedIPs(t *testing.T) {
	protection := &SSRFProtection{
		AllowPrivateIp:   false,
		DomainFilterMode: false,
		IpFilterMode:     false,
	}

	tests := []string{
		"127.0.0.1",
		"10.0.0.1",
		"169.254.169.254",
		"fc00::1",
		"::ffff:127.0.0.1",
	}
	for _, host := range tests {
		t.Run(host, func(t *testing.T) {
			require.Error(t, protection.ValidateNetworkTarget(host, 80))
		})
	}
}

func TestSSRFProtectionAllowsPrivateIPWhenExplicitlyEnabled(t *testing.T) {
	protection := &SSRFProtection{
		AllowPrivateIp:   true,
		DomainFilterMode: false,
		IpFilterMode:     false,
	}

	require.NoError(t, protection.ValidateNetworkTarget("10.0.0.1", 80))
}

func TestSSRFProtectionRejectsResolvedPrivateIP(t *testing.T) {
	protection := &SSRFProtection{
		AllowPrivateIp:         false,
		DomainFilterMode:       false,
		IpFilterMode:           false,
		ApplyIPFilterForDomain: true,
	}

	require.NoError(t, protection.ValidateNetworkTarget("example.com", 80))
	require.Error(t, protection.ValidateResolvedIP("example.com", net.ParseIP("169.254.169.254")))
}

func TestNewSSRFProtectionFromFetchSettingParsesPortRanges(t *testing.T) {
	protection, err := NewSSRFProtectionFromFetchSetting(false, false, false, nil, nil, []string{"80", "8000-8001"}, true)
	require.NoError(t, err)

	require.NoError(t, protection.ValidateNetworkTarget("example.com", 8001))
	require.Error(t, protection.ValidateNetworkTarget("example.com", 9000))
}

func TestParsePortRangesKeepsIntervalsCompact(t *testing.T) {
	ranges, err := parsePortRanges([]string{"80", "8000-9000", "1-65535"})
	require.NoError(t, err)
	require.Len(t, ranges, 3, "ranges must stay as intervals, not expand per port")
	require.Equal(t, PortRange{Start: 80, End: 80}, ranges[0])
	require.Equal(t, PortRange{Start: 8000, End: 9000}, ranges[1])
	require.Equal(t, PortRange{Start: 1, End: 65535}, ranges[2])
}

func TestIsAllowedPortRangeMembership(t *testing.T) {
	p := &SSRFProtection{AllowedPorts: []PortRange{{Start: 80, End: 80}, {Start: 8000, End: 9000}}}
	require.True(t, p.isAllowedPort(80))
	require.True(t, p.isAllowedPort(8000))
	require.True(t, p.isAllowedPort(8500))
	require.True(t, p.isAllowedPort(9000))
	require.False(t, p.isAllowedPort(81))
	require.False(t, p.isAllowedPort(9001))
}
