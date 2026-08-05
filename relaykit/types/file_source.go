package types

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
)

var fileSourceDiagnosticKey = func() [32]byte {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		panic("relaykit: initialize file-source diagnostic key: " + err.Error())
	}
	return key
}()

func fileSourceDiagnosticHMAC(data string) string {
	mac := hmac.New(sha256.New, fileSourceDiagnosticKey[:])
	_, _ = mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

// FileSource 统一的文件来源抽象接口
// 支持 URL 和 base64 两种来源，提供懒加载和缓存机制
type FileSource interface {
	IsURL() bool
	GetIdentifier() string
	GetRawData() string
	ClearRawData()

	SetCache(data *CachedFileData)
	GetCache() *CachedFileData
	HasCache() bool
	ClearCache()

	IsRegistered() bool
	SetRegistered(registered bool)
	Mu() *sync.Mutex
}

// baseFileSource 共享的缓存/锁/清理注册状态
type baseFileSource struct {
	cachedData  *CachedFileData
	cacheLoaded bool
	registered  bool
	mu          sync.Mutex
}

func (b *baseFileSource) SetCache(data *CachedFileData) {
	b.cachedData = data
	b.cacheLoaded = true
}

func (b *baseFileSource) GetCache() *CachedFileData {
	return b.cachedData
}

func (b *baseFileSource) HasCache() bool {
	return b.cacheLoaded && b.cachedData != nil
}

func (b *baseFileSource) ClearCache() {
	if b.cachedData != nil {
		b.cachedData.Close()
	}
	b.cachedData = nil
	b.cacheLoaded = false
}

func (b *baseFileSource) IsRegistered() bool {
	return b.registered
}

func (b *baseFileSource) SetRegistered(registered bool) {
	b.registered = registered
}

func (b *baseFileSource) Mu() *sync.Mutex {
	return &b.mu
}

// ---------------------------------------------------------------------------
// URLSource — URL 来源的 FileSource 实现
// ---------------------------------------------------------------------------

type URLSource struct {
	baseFileSource
	URL string
}

func (u *URLSource) IsURL() bool { return true }

func (u *URLSource) GetIdentifier() string {
	parsedURL, err := url.Parse(u.URL)
	if err != nil {
		return fmt.Sprintf("url:invalid[len=%d,hmac=%s]", len(u.URL), fileSourceDiagnosticHMAC(u.URL))
	}
	origin, ok := safeURLDiagnosticOrigin(parsedURL)
	if !ok {
		return fmt.Sprintf("url:invalid[len=%d,hmac=%s]", len(u.URL), fileSourceDiagnosticHMAC(u.URL))
	}
	return fmt.Sprintf(
		"url:%s/path-hmac=%s",
		origin,
		fileSourceDiagnosticHMAC(parsedURL.EscapedPath()),
	)
}

func safeURLDiagnosticOrigin(parsedURL *url.URL) (string, bool) {
	if parsedURL == nil {
		return "", false
	}
	if !strings.EqualFold(parsedURL.Scheme, "http") && !strings.EqualFold(parsedURL.Scheme, "https") {
		return "", false
	}

	rawHost := parsedURL.Host
	hostname := parsedURL.Hostname()
	port := parsedURL.Port()
	if hostname == "" || len(hostname) > 253 || strings.Contains(hostname, "%") || len(port) > 5 {
		return "", false
	}

	bracketed := strings.HasPrefix(rawHost, "[")
	if bracketed {
		closingBracket := strings.LastIndex(rawHost, "]")
		if closingBracket < 0 {
			return "", false
		}
		suffix := rawHost[closingBracket+1:]
		if suffix != "" && (port == "" || suffix != ":"+port) {
			return "", false
		}
		if suffix == "" && port != "" {
			return "", false
		}
	} else {
		switch strings.Count(rawHost, ":") {
		case 0:
			if port != "" {
				return "", false
			}
		case 1:
			if port == "" || strings.TrimSuffix(rawHost, ":"+port) != hostname {
				return "", false
			}
		default:
			return "", false
		}
	}

	portNumber := 0
	if port != "" {
		var err error
		portNumber, err = strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", false
		}
	}

	var safeHost string
	if ip := net.ParseIP(hostname); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			if bracketed {
				return "", false
			}
			safeHost = ipv4.String()
		} else {
			if !bracketed {
				return "", false
			}
			safeHost = "[" + ip.String() + "]"
		}
	} else {
		if bracketed {
			return "", false
		}
		safeHost = strings.TrimSuffix(strings.ToLower(hostname), ".")
		if safeHost == "" {
			return "", false
		}
		for _, label := range strings.Split(safeHost, ".") {
			if len(label) == 0 || len(label) > 63 {
				return "", false
			}
			for i := 0; i < len(label); i++ {
				character := label[i]
				isLetter := character >= 'a' && character <= 'z'
				isDigit := character >= '0' && character <= '9'
				isInteriorHyphen := character == '-' && i > 0 && i < len(label)-1
				if !isLetter && !isDigit && !isInteriorHyphen {
					return "", false
				}
			}
		}
	}

	if portNumber != 0 {
		safeHost += ":" + strconv.Itoa(portNumber)
	}
	return strings.ToLower(parsedURL.Scheme) + "://" + safeHost, true
}

func (u *URLSource) GetRawData() string { return u.URL }

func (u *URLSource) ClearRawData() {}

// ---------------------------------------------------------------------------
// Base64Source — Base64 内联数据来源的 FileSource 实现
// ---------------------------------------------------------------------------

type Base64Source struct {
	baseFileSource
	Base64Data string
	MimeType   string
}

func (b *Base64Source) IsURL() bool { return false }

func (b *Base64Source) GetIdentifier() string {
	return fmt.Sprintf("base64:len=%d,hmac=%s", len(b.Base64Data), fileSourceDiagnosticHMAC(b.Base64Data))
}

func (b *Base64Source) GetRawData() string { return b.Base64Data }

func (b *Base64Source) ClearRawData() {
	if len(b.Base64Data) > 1024 {
		b.Base64Data = ""
	}
}

// ---------------------------------------------------------------------------
// Constructors
// ---------------------------------------------------------------------------

func NewURLFileSource(url string) *URLSource {
	return &URLSource{URL: url}
}

func NewBase64FileSource(base64Data string, mimeType string) *Base64Source {
	return &Base64Source{
		Base64Data: base64Data,
		MimeType:   mimeType,
	}
}

func NewFileSourceFromData(data string, mimeType string) FileSource {
	if strings.HasPrefix(data, "http://") || strings.HasPrefix(data, "https://") {
		return NewURLFileSource(data)
	}
	return NewBase64FileSource(data, mimeType)
}

// ---------------------------------------------------------------------------
// CachedFileData — 缓存的文件数据（支持内存和磁盘两种模式）
// ---------------------------------------------------------------------------

type CachedFileData struct {
	base64Data  string        // 内存中的 base64 数据（小文件）
	MimeType    string        // MIME 类型
	Size        int64         // 文件大小（字节）
	DiskSize    int64         // 磁盘缓存实际占用大小（字节，通常是 base64 长度）
	ImageConfig *image.Config // 图片配置（如果是图片）
	ImageFormat string        // 图片格式（如果是图片）

	diskPath        string     // 磁盘缓存文件路径（大文件）
	isDisk          bool       // 是否使用磁盘缓存
	diskMu          sync.Mutex // 磁盘操作锁（保护磁盘文件的读取和删除）
	diskClosed      bool       // 是否已关闭/清理
	statDecremented bool       // 是否已扣减统计

	OnClose func(size int64)
}

func NewMemoryCachedData(base64Data string, mimeType string, size int64) *CachedFileData {
	return &CachedFileData{
		base64Data: base64Data,
		MimeType:   mimeType,
		Size:       size,
		isDisk:     false,
	}
}

func NewDiskCachedData(diskPath string, mimeType string, size int64) *CachedFileData {
	return &CachedFileData{
		diskPath: diskPath,
		MimeType: mimeType,
		Size:     size,
		isDisk:   true,
	}
}

func (c *CachedFileData) GetBase64Data() (string, error) {
	if !c.isDisk {
		return c.base64Data, nil
	}

	c.diskMu.Lock()
	defer c.diskMu.Unlock()

	if c.diskClosed {
		return "", fmt.Errorf("disk cache already closed")
	}

	data, err := os.ReadFile(c.diskPath)
	if err != nil {
		return "", fmt.Errorf("failed to read from disk cache: %w", err)
	}
	return string(data), nil
}

func (c *CachedFileData) SetBase64Data(data string) {
	if !c.isDisk {
		c.base64Data = data
	}
}

func (c *CachedFileData) IsDisk() bool {
	return c.isDisk
}

func (c *CachedFileData) Close() error {
	if !c.isDisk {
		c.base64Data = ""
		return nil
	}

	c.diskMu.Lock()
	defer c.diskMu.Unlock()

	if c.diskClosed {
		return nil
	}

	c.diskClosed = true
	if c.diskPath != "" {
		err := os.Remove(c.diskPath)
		if err == nil && !c.statDecremented && c.OnClose != nil {
			c.OnClose(c.DiskSize)
			c.statDecremented = true
		}
		return err
	}
	return nil
}
