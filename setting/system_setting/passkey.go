package system_setting

import (
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type PasskeySettings struct {
	Enabled              bool   `json:"enabled"`
	RPDisplayName        string `json:"rp_display_name"`
	RPID                 string `json:"rp_id"`
	Origins              string `json:"origins"`
	AllowInsecureOrigin  bool   `json:"allow_insecure_origin"`
	UserVerification     string `json:"user_verification"`
	AttachmentPreference string `json:"attachment_preference"`
}

var defaultPasskeySettings = PasskeySettings{
	Enabled:              false,
	RPDisplayName:        common.SystemName,
	RPID:                 "",
	Origins:              "",
	AllowInsecureOrigin:  false,
	UserVerification:     "preferred",
	AttachmentPreference: "",
}

func init() {
	config.GlobalConfig.Register("passkey", &defaultPasskeySettings)
}

func GetPasskeySettings() *PasskeySettings {
	current := config.Snapshot[PasskeySettings]("passkey")
	derived := *current
	if derived.RPID == "" && ServerAddress != "" {
		// 从ServerAddress提取域名作为RPID
		// ServerAddress可能是 "https://newapi.pro" 这种格式
		serverAddr := strings.TrimSpace(ServerAddress)
		if parsed, err := url.Parse(serverAddr); err == nil && parsed.Host != "" {
			derived.RPID = parsed.Host
		} else {
			derived.RPID = serverAddr
		}
	}
	if derived.Origins == "" || derived.Origins == "[]" {
		// Only treat ServerAddress as an explicit origin when it is an
		// https URL. Promoting a local http address (the default) to an
		// "explicit" origin bypassed the localhost development exception
		// in origin resolution and broke passkeys out of the box; leaving
		// Origins empty lets the request-based auto-detection apply its
		// localhost allowance.
		serverAddr := strings.TrimSpace(ServerAddress)
		if strings.HasPrefix(strings.ToLower(serverAddr), "https://") {
			derived.Origins = serverAddr
		} else {
			derived.Origins = ""
		}
	}
	return &derived
}
