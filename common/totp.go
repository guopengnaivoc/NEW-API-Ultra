package common

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	// 备用码配置
	BackupCodeLength = 8  // 备用码长度
	BackupCodeCount  = 10 // 生成备用码数量

	// 限制配置
	MaxFailAttempts = 5   // 最大失败尝试次数
	LockoutDuration = 300 // 锁定时间（秒）
)

// GenerateTOTPSecret 生成TOTP密钥和配置
func GenerateTOTPSecret(accountName string) (*otp.Key, error) {
	issuer := Get2FAIssuer()
	return totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
}

// MatchTOTPCounter 验证TOTP验证码并返回其命中的时间步计数器。
// 窗口与库默认一致（30秒周期，±1步偏移）；调用方可持久化该计数器
// 以拒绝同一时间步内的重放。
func MatchTOTPCounter(secret, code string, at time.Time) (int64, bool) {
	// 清理验证码格式
	cleanCode := strings.ReplaceAll(code, " ", "")
	if len(cleanCode) != 6 {
		return 0, false
	}

	base := at.Unix() / 30
	for _, offset := range []int64{0, -1, 1} {
		counter := base + offset
		ok, err := totp.ValidateCustom(cleanCode, secret, time.Unix(counter*30, 0), totp.ValidateOpts{
			Period:    30,
			Skew:      0,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && ok {
			return counter, true
		}
	}
	return 0, false
}

// GenerateBackupCodes 生成备用恢复码
func GenerateBackupCodes() ([]string, error) {
	codes := make([]string, 0, BackupCodeCount)
	seen := make(map[string]bool, BackupCodeCount)

	for len(codes) < BackupCodeCount {
		code, err := generateRandomBackupCode()
		if err != nil {
			return nil, err
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code)
	}

	return codes, nil
}

// generateRandomBackupCode 生成单个备用码
func generateRandomBackupCode() (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	code := make([]byte, BackupCodeLength)

	for i := range code {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		code[i] = charset[idx.Int64()]
	}

	// 格式化为 XXXX-XXXX 格式
	return fmt.Sprintf("%s-%s", string(code[:4]), string(code[4:])), nil
}

// ValidateBackupCode 验证备用码格式
func ValidateBackupCode(code string) bool {
	// 移除所有分隔符并转为大写
	cleanCode := strings.ToUpper(strings.ReplaceAll(code, "-", ""))
	if len(cleanCode) != BackupCodeLength {
		return false
	}

	// 检查字符是否合法
	for _, char := range cleanCode {
		if !((char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')) {
			return false
		}
	}

	return true
}

// NormalizeBackupCode 标准化备用码格式
func NormalizeBackupCode(code string) string {
	cleanCode := strings.ToUpper(strings.ReplaceAll(code, "-", ""))
	if len(cleanCode) == BackupCodeLength {
		return fmt.Sprintf("%s-%s", cleanCode[:4], cleanCode[4:])
	}
	return code
}

// HashBackupCode 对备用码进行哈希
func HashBackupCode(code string) (string, error) {
	normalizedCode := NormalizeBackupCode(code)
	return Password2Hash(normalizedCode)
}

// Get2FAIssuer 获取2FA发行者名称
func Get2FAIssuer() string {
	return SystemName
}

// getEnvOrDefault 获取环境变量或默认值
func getEnvOrDefault(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// ValidateNumericCode 验证数字验证码格式
func ValidateNumericCode(code string) (string, error) {
	// 移除空格
	code = strings.ReplaceAll(code, " ", "")

	if len(code) != 6 {
		return "", fmt.Errorf("验证码必须是6位数字")
	}

	// 检查是否为纯数字
	if _, err := strconv.Atoi(code); err != nil {
		return "", fmt.Errorf("验证码只能包含数字")
	}

	return code, nil
}

// GenerateQRCodeData 生成二维码数据
func GenerateQRCodeData(secret, username string) string {
	issuer := Get2FAIssuer()
	accountName := fmt.Sprintf("%s (%s)", username, issuer)
	label := url.PathEscape(fmt.Sprintf("%s:%s", issuer, accountName))
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("digits", "6")
	query.Set("period", "30")
	return fmt.Sprintf("otpauth://totp/%s?%s", label, query.Encode())
}
