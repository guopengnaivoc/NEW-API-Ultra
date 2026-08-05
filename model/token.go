package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const TokenDisplayPrefixLength = 8

type Token struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index"`
	Key                string         `json:"-" gorm:"-"`
	KeyHash            string         `json:"-" gorm:"column:key;type:varchar(128);uniqueIndex"`
	KeyPrefix          string         `json:"-" gorm:"type:varchar(16);index"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index" `
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           *string        `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"` // used quota
	Group              string         `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool           `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

func HashTokenKey(key string) string {
	digest := sha256.Sum256([]byte(key))
	return hex.EncodeToString(digest[:])
}

func TokenKeyPrefix(key string) string {
	if len(key) <= TokenDisplayPrefixLength {
		return key
	}
	return key[:TokenDisplayPrefixLength]
}

func MaskTokenKey(prefix string) string {
	if prefix == "" {
		return ""
	}
	return prefix + "**********"
}

func (token *Token) GetMaskedKey() string {
	prefix := token.KeyPrefix
	if prefix == "" && token.Key != "" {
		prefix = TokenKeyPrefix(token.Key)
	}
	return MaskTokenKey(prefix)
}

func (token *Token) IsKeyInvalidated() bool {
	return token.KeyPrefix == invalidTokenKeyPrefix
}

func (token *Token) BeforeCreate(_ *gorm.DB) error {
	if token.Key == "" {
		return errors.New("token key is required")
	}
	token.KeyHash = HashTokenKey(token.Key)
	token.KeyPrefix = TokenKeyPrefix(token.Key)
	return nil
}

func (token *Token) BeforeUpdate(_ *gorm.DB) error {
	if token.Key != "" {
		token.KeyHash = HashTokenKey(token.Key)
		token.KeyPrefix = TokenKeyPrefix(token.Key)
	}
	return nil
}

func (token *Token) GetIpLimits() []string {
	// delete empty spaces
	//split with \n
	ipLimits := make([]string, 0)
	if token.AllowIps == nil {
		return ipLimits
	}
	cleanIps := strings.ReplaceAll(*token.AllowIps, " ", "")
	if cleanIps == "" {
		return ipLimits
	}
	ips := strings.Split(cleanIps, "\n")
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		ip = strings.ReplaceAll(ip, ",", "")
		if ip != "" {
			ipLimits = append(ipLimits, ip)
		}
	}
	return ipLimits
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

// sanitizeLikePattern 校验并清洗用户输入的 LIKE 搜索模式。
// 规则：
//  1. 转义 ! 和 _（使用 ! 作为 ESCAPE 字符，兼容 MySQL/PostgreSQL/SQLite）
//  2. 连续的 % 合并为单个 %
//  3. 最多允许 2 个 %
//  4. 含 % 时（模糊搜索），去掉 % 后关键词长度必须 >= 2
//  5. 不含 % 时按精确匹配
func sanitizeLikePattern(input string) (string, error) {
	// 1. 先转义 ESCAPE 字符 ! 自身，再转义 _
	//    使用 ! 而非 \ 作为 ESCAPE 字符，避免 MySQL 中反斜杠的字符串转义问题
	input = strings.ReplaceAll(input, "!", "!!")
	input = strings.ReplaceAll(input, `_`, `!_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}

	// 5. 无 % 时，精确全匹配
	return input, nil
}

func validateLikePattern(input string) error {
	// 1. 连续的 % 直接拒绝
	if strings.Contains(input, "%%") {
		return errors.New("搜索模式中不允许包含连续的 % 通配符")
	}

	// 2. 统计 % 数量，不得超过 2
	count := strings.Count(input, "%")
	if count > 2 {
		return errors.New("搜索模式中最多允许包含 2 个 % 通配符")
	}

	// 3. 含 % 时，去掉 % 后关键词长度必须 >= 2
	if count > 0 {
		stripped := strings.ReplaceAll(input, "%", "")
		if len(stripped) < 2 {
			return errors.New("使用模糊搜索时，关键词长度至少为 2 个字符")
		}
	}

	return nil
}

const searchHardLimit = 100

func SearchUserTokens(userId int, keyword string, token string, offset int, limit int) (tokens []*Token, total int64, err error) {
	// model 层强制截断
	if limit <= 0 || limit > searchHardLimit {
		limit = searchHardLimit
	}
	if offset < 0 {
		offset = 0
	}

	if token != "" {
		token = strings.TrimPrefix(token, "sk-")
	}

	// 超量用户（令牌数超过上限）只允许精确搜索，禁止模糊搜索
	maxTokens := operation_setting.GetMaxUserTokens()
	hasFuzzy := strings.Contains(keyword, "%") || strings.Contains(token, "%")
	if hasFuzzy {
		count, err := CountUserTokens(userId)
		if err != nil {
			common.SysLog("failed to count user tokens: " + err.Error())
			return nil, 0, errors.New("获取令牌数量失败")
		}
		if int(count) > maxTokens {
			return nil, 0, errors.New("令牌数量超过上限，仅允许精确搜索，请勿使用 % 通配符")
		}
	}

	baseQuery := DB.Model(&Token{}).Where("user_id = ?", userId)

	// 非空才加 LIKE 条件，空则跳过（不过滤该字段）
	if keyword != "" {
		keywordPattern, err := sanitizeLikePattern(keyword)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where("name LIKE ? ESCAPE '!'", keywordPattern)
	}
	if token != "" {
		if !strings.Contains(token, "%") && len(token) > TokenDisplayPrefixLength {
			baseQuery = baseQuery.Where(commonKeyCol+" = ?", HashTokenKey(token))
		} else {
			tokenPattern, err := sanitizeLikePattern(token)
			if err != nil {
				return nil, 0, err
			}
			baseQuery = baseQuery.Where("key_prefix LIKE ? ESCAPE '!'", tokenPattern)
		}
	}

	// 先查匹配总数（用于分页，受 maxTokens 上限保护，避免全表 COUNT）
	err = baseQuery.Limit(maxTokens).Count(&total).Error
	if err != nil {
		common.SysError("failed to count search tokens: " + err.Error())
		return nil, 0, errors.New("搜索令牌失败")
	}

	// 再分页查数据
	err = baseQuery.Order("id desc").Offset(offset).Limit(limit).Find(&tokens).Error
	if err != nil {
		common.SysError("failed to search tokens: " + err.Error())
		return nil, 0, errors.New("搜索令牌失败")
	}
	return tokens, total, nil
}

func ValidateUserToken(key string) (token *Token, err error) {
	if key == "" {
		return nil, ErrTokenNotProvided
	}
	token, err = GetTokenByKey(key, false)
	if err == nil {
		if token.IsKeyInvalidated() ||
			token.Status == common.TokenStatusExhausted ||
			token.Status == common.TokenStatusExpired ||
			token.Status != common.TokenStatusEnabled {
			return token, ErrTokenInvalid
		}
		if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
			return resolveTokenInvalidStatus(token, common.TokenStatusExpired)
		}
		if !token.UnlimitedQuota && token.RemainQuota <= 0 {
			return resolveTokenInvalidStatus(token, common.TokenStatusExhausted)
		}
		return token, nil
	}
	common.SysLog("ValidateUserToken: failed to get token: " + err.Error())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTokenInvalid
	}
	return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
}

// resolveTokenInvalidStatus 条件化落库失效状态，然后按数据库权威状态给出裁决。
// 快照判定失效不代表数据库仍然失效：并发退款可能已经恢复了额度，此时守卫
// UPDATE 不会命中，重读到的 token 是可用的，必须放行而不是拒绝。
func resolveTokenInvalidStatus(token *Token, status int) (*Token, error) {
	current := persistTokenInvalidStatus(token, status)
	if current.Status == common.TokenStatusEnabled &&
		!current.IsKeyInvalidated() &&
		(current.ExpiredTime == -1 || current.ExpiredTime >= common.GetTimestamp()) &&
		(current.UnlimitedQuota || current.RemainQuota > 0) {
		return current, nil
	}
	return current, ErrTokenInvalid
}

// testHookBeforeTokenStatusPersist 若非 nil，会在条件状态写入前被调用。
// 它只为回归测试提供确定性交错点（在鉴权快照读取与状态落库之间插入一次退款），
// 生产代码中恒为 nil。
var testHookBeforeTokenStatusPersist func(tokenId int, status int)

// persistTokenInvalidStatus 以数据库当前状态为守卫，条件化地把 token 落为
// Expired / Exhausted，并返回与数据库一致的权威状态。
//
// 绝不能信任调用方手里的内存快照：快照读取与状态写入之间，退款可能已经提交。
// 无条件写入会把 remain_quota>0 的 token 覆盖成 Exhausted，使其被永久拒绝。
// 因此写入条件全部放在同一条 UPDATE 的 WHERE 里：仅当数据库中的记录此刻仍满足
// 对应的失效条件（状态仍为 Enabled、密钥未失效、未被软删除，且 Exhausted 还要求
// 非无限额度、额度确实 <= 0、并且不该优先判定为 Expired）时才会命中。
// 未命中（RowsAffected == 0）说明数据库已经前进，重读并返回权威状态，
// 绝不返回本地构造的失效副本。
//
// 写入与重读都必须绑定鉴权时读到的 KeyHash，而不能只按 id：id 标识的是行，
// KeyHash 标识的才是本次请求出示的凭据。并发 RotateTokenKey 提交后，同一行
// 已属于新凭据；只按 id 操作会把旧 key 请求的状态写进新凭据行，或把新凭据行
// 当作权威状态返回给旧 key 请求，使已轮换的 key 重新通过鉴权。id + KeyHash
// 查不到行即凭据已被轮换或删除，必须 fail closed 返回失效。
//
// 同样不能原地修改共享的 token 结构体：GetTokenByKey 的异步缓存回填 goroutine
// 会读取它，原地写状态是数据竞争。
func persistTokenInvalidStatus(token *Token, status int) *Token {
	if testHookBeforeTokenStatusPersist != nil {
		testHookBeforeTokenStatusPersist(token.Id, status)
	}
	now := common.GetTimestamp()
	query := DB.Model(&Token{}).
		Where("id = ?", token.Id).
		Where(commonKeyCol+" = ?", token.KeyHash).
		Where("status = ?", common.TokenStatusEnabled).
		Where("key_prefix <> ?", invalidTokenKeyPrefix).
		Where("deleted_at IS NULL")
	switch status {
	case common.TokenStatusExpired:
		query = query.
			Where("expired_time <> ?", -1).
			Where("expired_time < ?", now)
	case common.TokenStatusExhausted:
		query = query.
			Where("unlimited_quota = ?", false).
			Where("remain_quota <= ?", 0).
			Where("expired_time = ? OR expired_time > ?", -1, now)
	default:
		return token
	}
	result := query.Updates(map[string]interface{}{
		"status":        status,
		"accessed_time": now,
	})
	if result.Error != nil {
		common.SysLog("failed to update token status: " + result.Error.Error())
	} else if result.RowsAffected > 0 {
		persisted := *token
		persisted.Status = status
		persisted.AccessedTime = now
		return &persisted
	}
	// 守卫未命中或写入失败：数据库状态已与快照不同（例如退款刚恢复了额度）。
	// 重读权威状态返回，绝不把快照推导出的失效状态呈现给调用方。
	// 重读同样绑定 id + 原始 KeyHash：查不到即本次请求出示的凭据已被轮换或
	// 删除，返回失效快照 fail closed，绝不读取（更不能回填明文 key 到）已经
	// 属于新凭据的行。
	var current Token
	err := DB.Where("id = ? AND "+commonKeyCol+" = ?", token.Id, token.KeyHash).First(&current).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			common.SysLog("failed to reload token after conditional status update: " + err.Error())
		}
		return token
	}
	current.Key = token.Key
	return &current
}

func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	var err error = nil
	err = DB.First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	token := Token{Id: id}
	var err error = nil
	err = DB.First(&token, "id = ?", id).Error
	if shouldUpdateRedis(true, err) {
		gopool.Go(func() {
			if err := cacheSetToken(token); err != nil {
				common.SysLog("failed to update user status cache: " + err.Error())
			}
		})
	}
	return &token, err
}

func GetTokenByKey(key string, fromDB bool) (token *Token, err error) {
	keyHash := HashTokenKey(key)
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) && token != nil {
			gopool.Go(func() {
				if err := cacheSetToken(*token); err != nil {
					common.SysLog("failed to update user status cache: " + err.Error())
				}
			})
		}
	}()
	if !fromDB && common.RedisEnabled {
		// Try Redis first
		token, err := cacheGetTokenByKey(key)
		if err == nil {
			if err = loadTokenAuthorizationState(token); err == nil {
				return token, nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
		}
		// Don't return error - fall through to DB
	}
	fromDB = true
	var current Token
	err = DB.Where(commonKeyCol+" = ?", keyHash).First(&current).Error
	if err == nil {
		current.Key = key
		token = &current
	} else {
		token = nil
	}
	return token, err
}

func loadTokenAuthorizationState(token *Token) error {
	var current Token
	if err := DB.Where("id = ? AND "+commonKeyCol+" = ?", token.Id, token.KeyHash).First(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if cacheErr := cacheDeleteTokenHash(token.KeyHash); cacheErr != nil {
				common.SysLog("failed to delete stale token cache: " + cacheErr.Error())
			}
		}
		return err
	}
	current.Key = token.Key
	*token = current
	return nil
}

func (token *Token) Insert() error {
	var err error
	err = DB.Create(token).Error
	return err
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() (err error) {
	defer func() {
		if shouldUpdateRedis(true, err) {
			gopool.Go(func() {
				err := cacheSetToken(*token)
				if err != nil {
					common.SysLog("failed to update token cache: " + err.Error())
				}
			})
		}
	}()
	err = DB.Model(token).Select("name", "status", "expired_time", "remain_quota", "unlimited_quota",
		"model_limits_enabled", "model_limits", "allow_ips", "group", "cross_group_retry").Updates(token).Error
	return err
}

func (token *Token) SelectUpdate() (err error) {
	defer func() {
		if shouldUpdateRedis(true, err) {
			gopool.Go(func() {
				err := cacheSetToken(*token)
				if err != nil {
					common.SysLog("failed to update token cache: " + err.Error())
				}
			})
		}
	}()
	// This can update zero values
	return DB.Model(token).Select("accessed_time", "status").Updates(token).Error
}

func (token *Token) Delete() (err error) {
	defer func() {
		if shouldUpdateRedis(true, err) {
			gopool.Go(func() {
				err := cacheDeleteTokenHash(token.KeyHash)
				if err != nil {
					common.SysLog("failed to delete token cache: " + err.Error())
				}
			})
		}
	}()
	err = DB.Delete(token).Error
	return err
}

func RotateTokenKey(id int, userId int, key string) (*Token, error) {
	if id <= 0 || userId <= 0 || key == "" {
		return nil, errors.New("invalid token rotation request")
	}

	var (
		token      Token
		oldKeyHash string
	)
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).
			Where("id = ? AND user_id = ?", id, userId).
			First(&token).Error; err != nil {
			return err
		}
		oldKeyHash = token.KeyHash
		token.Key = key
		token.KeyHash = HashTokenKey(key)
		token.KeyPrefix = TokenKeyPrefix(key)
		return tx.Model(&Token{}).
			Where("id = ? AND user_id = ?", id, userId).
			Updates(map[string]any{
				"key":        token.KeyHash,
				"key_prefix": token.KeyPrefix,
			}).Error
	})
	if err != nil {
		return nil, err
	}

	if common.RedisEnabled {
		if err := cacheDeleteTokenHash(oldKeyHash); err != nil {
			common.SysLog("failed to delete rotated token cache: " + err.Error())
		}
		if err := cacheSetToken(token); err != nil {
			common.SysLog("failed to cache rotated token: " + err.Error())
		}
	}
	return &token, nil
}

func (token *Token) IsModelLimitsEnabled() bool {
	return token.ModelLimitsEnabled
}

func (token *Token) GetModelLimits() []string {
	if token.ModelLimits == "" {
		return []string{}
	}
	return strings.Split(token.ModelLimits, ",")
}

func (token *Token) GetModelLimitsMap() map[string]bool {
	limits := token.GetModelLimits()
	limitsMap := make(map[string]bool)
	for _, limit := range limits {
		limitsMap[limit] = true
	}
	return limitsMap
}

func DisableModelLimits(tokenId int) error {
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	return token.Update()
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	err = DB.Where(token).First(&token).Error
	if err != nil {
		return err
	}
	return token.Delete()
}

func IncreaseTokenQuota(tokenId int, _ string, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	return increaseTokenQuota(tokenId, quota)
}

func increaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota + ?", quota),
			"used_quota":    gorm.Expr("used_quota - ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	if err != nil {
		return err
	}
	if quota > 0 {
		return RestoreExhaustedTokenIfFunded(DB, id)
	}
	return nil
}

// RestoreExhaustedTokenIfFunded 在退款使一个已耗尽的 token 重新有额度时把它恢复为可用。
// ValidateUserToken 会把额度耗尽的 token 落库为 TokenStatusExhausted，而此前没有任何
// 代码路径会写回 TokenStatusEnabled，因此退款后的 token 会永久失效。
//
// 恢复条件全部写在同一条 UPDATE 的 WHERE 里，不做「先读后写」：
// 只有当该 token 此刻仍处于耗尽状态、确实有正额度、密钥未失效、未过期且未被
// 软删除时才会命中，因此被禁用、已过期、已失效、已删除或额度仍为 0 的 token
// 不会被复活；并发消费把额度重新打到 0 时该语句会匹配 0 行，token 保持耗尽状态。
// deleted_at 条件显式写出，因为部分退款事务（billing operation / MJ 预扣）
// 沿用其兄弟语句的 Unscoped 会话，不能依赖 GORM 的软删除默认作用域。
// 所有会给 token 回补 remain_quota 的路径都必须调用它，否则退款后 token 仍然不可用。
func RestoreExhaustedTokenIfFunded(tx *gorm.DB, id int) error {
	return tx.Model(&Token{}).
		Where("id = ?", id).
		Where("status = ?", common.TokenStatusExhausted).
		Where("remain_quota > ?", 0).
		Where("key_prefix <> ?", invalidTokenKeyPrefix).
		Where("expired_time = ? OR expired_time > ?", -1, common.GetTimestamp()).
		Where("deleted_at IS NULL").
		Update("status", common.TokenStatusEnabled).Error
}

func DecreaseTokenQuota(id int, _ string, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	return decreaseTokenQuota(id, quota)
}

func decreaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

// CountUserTokens returns total number of tokens for the given user, used for pagination
func CountUserTokens(userId int) (int64, error) {
	var total int64
	err := DB.Model(&Token{}).Where("user_id = ?", userId).Count(&total).Error
	return total, err
}

// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, errors.New("ids 不能为空！")
	}

	tx := DB.Begin()

	var tokens []Token
	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Find(&tokens).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Delete(&Token{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	if common.RedisEnabled {
		gopool.Go(func() {
			for _, t := range tokens {
				_ = cacheDeleteTokenHash(t.KeyHash)
			}
		})
	}

	return len(tokens), nil
}

// InvalidateUserTokensCache 清理指定用户所有令牌在 Redis 中的缓存，
// 配合 InvalidateUserCache 使用，可在用户被禁用/删除时立即阻断其令牌的请求。
// 下一次请求将从数据库重新加载令牌及用户状态，从而立即识别出被禁用的用户。
func InvalidateUserTokensCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return errors.New("userId 无效")
	}
	var tokens []Token
	if err := DB.Unscoped().
		Select("id", commonKeyCol).
		Where("user_id = ?", userId).
		Find(&tokens).Error; err != nil {
		return err
	}
	return invalidateTokensCache(tokens)
}

func invalidateTokensCache(tokens []Token) error {
	if !common.RedisEnabled {
		return nil
	}
	var firstErr error
	for _, t := range tokens {
		if t.KeyHash == "" {
			continue
		}
		if err := cacheDeleteTokenHash(t.KeyHash); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
