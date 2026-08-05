package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Upstream model and pricing metadata is written straight into the local model
// catalogue and into billing ratios, so whoever controls those documents
// controls business data. Transport security alone is a weak guarantee for
// that: it attests who served the bytes, not that the bytes are the ones the
// operator reviewed, and it collapses entirely when TLS_INSECURE_SKIP_VERIFY
// is set. Operators who need the stronger guarantee pin the exact document
// with SYNC_UPSTREAM_DIGESTS="<url>=<sha256-hex>,..."; a third-party source
// that is not pinned must at least arrive over HTTPS.
const upstreamMetadataDigestEnv = "SYNC_UPSTREAM_DIGESTS"

// upstreamMetadataDigestPins fails closed on a malformed entry rather than
// dropping it: an operator who mistyped a pin would otherwise silently fall
// back to the weaker transport-only check they were trying to escape.
func upstreamMetadataDigestPins() (map[string]string, error) {
	raw := strings.TrimSpace(common.GetEnvOrDefaultString(upstreamMetadataDigestEnv, ""))
	if raw == "" {
		return nil, nil
	}
	pins := make(map[string]string)
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		target, digest, found := strings.Cut(entry, "=")
		target = strings.TrimSpace(target)
		digest = strings.ToLower(strings.TrimSpace(digest))
		if !found || target == "" {
			return nil, fmt.Errorf("%s entry %q must be formatted as <url>=<sha256-hex>", upstreamMetadataDigestEnv, entry)
		}
		if len(digest) != sha256.Size*2 {
			return nil, fmt.Errorf("%s entry for %s must carry a %d-character sha256 hex digest", upstreamMetadataDigestEnv, target, sha256.Size*2)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, fmt.Errorf("%s entry for %s is not valid hex: %w", upstreamMetadataDigestEnv, target, err)
		}
		pins[target] = digest
	}
	return pins, nil
}

// checkUpstreamMetadataSource runs before the request is issued so an unusable
// source fails fast and visibly instead of being retried three times.
func checkUpstreamMetadataSource(rawURL string) error {
	pins, err := upstreamMetadataDigestPins()
	if err != nil {
		return err
	}
	if _, pinned := pins[rawURL]; pinned {
		return nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid upstream metadata URL %q: %w", rawURL, err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("upstream metadata source %q must use https, or carry a pinned digest in %s", rawURL, upstreamMetadataDigestEnv)
	}
	if common.TLSInsecureSkipVerify {
		common.SysError("TLS_INSECURE_SKIP_VERIFY=true leaves upstream metadata " + rawURL + " unauthenticated; pin it with " + upstreamMetadataDigestEnv)
	}
	return nil
}

// checkUpstreamMetadataBody must be called before the document is decoded,
// cached, or compared against local pricing, so a poisoned body never reaches
// business data even once.
func checkUpstreamMetadataBody(rawURL string, body []byte) error {
	pins, err := upstreamMetadataDigestPins()
	if err != nil {
		return err
	}
	pinned, ok := pins[rawURL]
	if !ok {
		return nil
	}
	sum := sha256.Sum256(body)
	if actual := hex.EncodeToString(sum[:]); actual != pinned {
		return fmt.Errorf("upstream metadata digest mismatch for %s: pinned %s, received %s", rawURL, pinned, actual)
	}
	return nil
}

// isSupportedUpstreamScheme replaces a bare strings.HasPrefix(u, "http") test,
// which also accepted strings like "httpx://" and bare "https" hostnames.
func isSupportedUpstreamScheme(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

// isShippedMetadataPresetHost reports whether the URL points at one of the
// third-party pricing documents this project ships as a preset. Operator-owned
// channels may legitimately sit on plaintext inside a private network, but
// these two are public sources of truth for billing and get the stricter rule.
func isShippedMetadataPresetHost(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == officialRatioPresetHost || host == modelsDevHost
}
