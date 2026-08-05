package controller

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type tokenAPIResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type tokenPageResponse struct {
	Items []tokenResponseItem `json:"items"`
}

type tokenResponseItem struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Key    string `json:"key"`
	Status int    `json:"status"`
}

type tokenKeyResponse struct {
	Token tokenResponseItem `json:"token"`
	Key   string            `json:"key"`
}

type sqliteColumnInfo struct {
	Name string `gorm:"column:name"`
	Type string `gorm:"column:type"`
}

type legacyToken struct {
	Id                 int    `gorm:"primaryKey"`
	UserId             int    `gorm:"index"`
	Key                string `gorm:"column:key;type:char(48);uniqueIndex"`
	Status             int    `gorm:"default:1"`
	Name               string `gorm:"index"`
	CreatedTime        int64  `gorm:"bigint"`
	AccessedTime       int64  `gorm:"bigint"`
	ExpiredTime        int64  `gorm:"bigint;default:-1"`
	RemainQuota        int    `gorm:"default:0"`
	UnlimitedQuota     bool
	ModelLimitsEnabled bool
	ModelLimits        string  `gorm:"type:text"`
	AllowIps           *string `gorm:"default:''"`
	UsedQuota          int     `gorm:"default:0"`
	Group              string  `gorm:"column:group;default:''"`
	CrossGroupRetry    bool
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

func (legacyToken) TableName() string {
	return "tokens"
}

func openTokenControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite db: %v", err)
	}
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func migrateTokenControllerTestDB(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.AutoMigrate(&model.Token{}); err != nil {
		t.Fatalf("failed to migrate token table: %v", err)
	}
	require.NoError(t, model.MigrateTokenKeys())
}

func setupTokenControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTokenControllerTestDB(t)
	migrateTokenControllerTestDB(t, db)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.AuthFlow{}))
	return db
}

func openTokenControllerExternalDB(t *testing.T, dialect string, dsn string) (*gorm.DB, *bool) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.RedisEnabled = false

	var (
		db     *gorm.DB
		dbType common.DatabaseType
		err    error
	)
	switch dialect {
	case "mysql":
		dbType = common.DatabaseTypeMySQL
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	case "postgres":
		dbType = common.DatabaseTypePostgreSQL
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	default:
		t.Fatalf("unsupported dialect %q", dialect)
	}
	common.SetDatabaseTypes(dbType, dbType)
	if err != nil {
		t.Fatalf("failed to open %s db: %v", dialect, err)
	}

	model.DB = db
	model.LOG_DB = db

	if db.Migrator().HasTable("tokens") {
		t.Skipf("refusing to run %s migration compatibility test against external database because tokens table already exists", dialect)
	}

	managedTokensTable := new(bool)

	t.Cleanup(func() {
		if *managedTokensTable && db.Migrator().HasTable("tokens") {
			_ = db.Migrator().DropTable("tokens")
		}
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db, managedTokensTable
}

func seedToken(t *testing.T, db *gorm.DB, userID int, name string, rawKey string) *model.Token {
	t.Helper()

	token := &model.Token{
		UserId:         userID,
		Name:           name,
		Key:            rawKey,
		Status:         common.TokenStatusEnabled,
		CreatedTime:    1,
		AccessedTime:   1,
		ExpiredTime:    -1,
		RemainQuota:    100,
		UnlimitedQuota: true,
		Group:          "default",
	}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	return token
}

func newAuthenticatedContext(t *testing.T, method string, target string, body any, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	var requestBody *bytes.Reader
	if body != nil {
		payload, err := common.Marshal(body)
		if err != nil {
			t.Fatalf("failed to marshal request body: %v", err)
		}
		requestBody = bytes.NewReader(payload)
	} else {
		requestBody = bytes.NewReader(nil)
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, requestBody)
	if body != nil {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	ctx.Set("id", userID)
	return ctx, recorder
}

func addTokenKeyRotateProof(t *testing.T, db *gorm.DB, ctx *gin.Context, userID int, target string) string {
	t.Helper()
	common.SessionSecret = "token-key-proof-test-secret"
	user := model.User{
		Id: userID, Username: fmt.Sprintf("token-proof-user-%d", userID),
		Password: "unused-password-hash", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
		AffCode: fmt.Sprintf("token-proof-aff-%d", userID), AuthVersion: 1,
	}
	require.NoError(t, db.FirstOrCreate(&user, "id = ?", userID).Error)
	session := model.UserSession{
		SID:    fmt.Sprintf("token-key-proof-session-%d-%s", userID, target),
		UserID: userID, Version: 1, UserAuthVersion: user.AuthVersion,
		Status: model.UserSessionStatusActive, RefreshHash: "refresh-hash",
		ExpiresAt: 4102444800,
	}
	require.NoError(t, db.FirstOrCreate(&session, "sid = ?", session.SID).Error)
	identity := service.AuthIdentity{
		UserID: userID, SessionID: session.SID,
		UserAuthVersion: user.AuthVersion, SessionVersion: session.Version,
	}
	proof, _, err := service.IssueOneTimeSecurityProof(
		identity,
		"password",
		securityProofScopeTokenKeyRotate,
		target,
	)
	require.NoError(t, err)
	ctx.Set("session_id", identity.SessionID)
	ctx.Set("auth_version", identity.UserAuthVersion)
	ctx.Set("session_version", identity.SessionVersion)
	ctx.Set("auth_identity", identity)
	ctx.Request.Header.Set("X-Security-Proof", proof)
	return proof
}

func decodeAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) tokenAPIResponse {
	t.Helper()

	var response tokenAPIResponse
	if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode api response: %v", err)
	}
	return response
}

func getSQLiteColumnType(t *testing.T, db *gorm.DB, tableName string, columnName string) string {
	t.Helper()

	var columns []sqliteColumnInfo
	if err := db.Raw("PRAGMA table_info(" + tableName + ")").Scan(&columns).Error; err != nil {
		t.Fatalf("failed to inspect %s schema: %v", tableName, err)
	}

	for _, column := range columns {
		if column.Name == columnName {
			return strings.ToLower(column.Type)
		}
	}

	t.Fatalf("column %s not found in %s schema", columnName, tableName)
	return ""
}

func getTokenKeyColumnType(t *testing.T, db *gorm.DB, dialect string) string {
	t.Helper()

	switch dialect {
	case "sqlite":
		return getSQLiteColumnType(t, db, "tokens", "key")
	case "mysql":
		var columnType string
		if err := db.Raw(`SELECT COLUMN_TYPE FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
			"tokens", "key").Scan(&columnType).Error; err != nil {
			t.Fatalf("failed to inspect mysql token key column: %v", err)
		}
		return strings.ToLower(columnType)
	case "postgres":
		var dataType string
		var maxLength sql.NullInt64
		if err := db.Raw(`SELECT data_type, character_maximum_length
			FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`,
			"tokens", "key").Row().Scan(&dataType, &maxLength); err != nil {
			t.Fatalf("failed to inspect postgres token key column: %v", err)
		}
		switch strings.ToLower(dataType) {
		case "character varying":
			return fmt.Sprintf("varchar(%d)", maxLength.Int64)
		case "character":
			return fmt.Sprintf("char(%d)", maxLength.Int64)
		default:
			if maxLength.Valid {
				return fmt.Sprintf("%s(%d)", strings.ToLower(dataType), maxLength.Int64)
			}
			return strings.ToLower(dataType)
		}
	default:
		t.Fatalf("unsupported dialect %q", dialect)
		return ""
	}
}

func runTokenMigrationCompatibilityTest(t *testing.T, db *gorm.DB, dialect string, managedTokensTable *bool) {
	t.Helper()

	legacyKey := strings.Repeat("a", 48)
	longKey := strings.Repeat("b", 64)

	if err := db.AutoMigrate(&legacyToken{}); err != nil {
		t.Fatalf("failed to create legacy token schema: %v", err)
	}
	if managedTokensTable != nil {
		*managedTokensTable = true
	}
	if err := db.Create(&legacyToken{
		UserId:             7,
		Key:                legacyKey,
		Status:             common.TokenStatusEnabled,
		Name:               "legacy-token",
		CreatedTime:        1,
		AccessedTime:       1,
		ExpiredTime:        -1,
		RemainQuota:        100,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: false,
		ModelLimits:        "",
		AllowIps:           common.GetPointer(""),
		UsedQuota:          0,
		Group:              "default",
		CrossGroupRetry:    false,
	}).Error; err != nil {
		t.Fatalf("failed to seed legacy token row: %v", err)
	}

	if got := getTokenKeyColumnType(t, db, dialect); got != "char(48)" {
		t.Fatalf("expected legacy key column type char(48), got %q", got)
	}

	migrateTokenControllerTestDB(t, db)
	require.NoError(t, model.MigrateTokenKeys())

	if got := getTokenKeyColumnType(t, db, dialect); got != "varchar(128)" {
		t.Fatalf("expected migrated key column type varchar(128), got %q", got)
	}

	var migratedToken model.Token
	if err := db.First(&migratedToken, "name = ?", "legacy-token").Error; err != nil {
		t.Fatalf("failed to load migrated token row: %v", err)
	}
	assert.Empty(t, migratedToken.Key)
	assert.Equal(t, model.HashTokenKey(legacyKey), migratedToken.KeyHash)
	assert.Equal(t, model.TokenKeyPrefix(legacyKey), migratedToken.KeyPrefix)
	if migratedToken.Name != "legacy-token" {
		t.Fatalf("expected migrated token name to be preserved, got %q", migratedToken.Name)
	}
	authenticated, err := model.GetTokenByKey(legacyKey, true)
	require.NoError(t, err)
	assert.Equal(t, migratedToken.Id, authenticated.Id)
	assert.Equal(t, legacyKey, authenticated.Key)

	require.NoError(t, model.MigrateTokenKeys())
	var idempotent model.Token
	require.NoError(t, db.First(&idempotent, migratedToken.Id).Error)
	assert.Equal(t, migratedToken.KeyHash, idempotent.KeyHash)
	assert.Equal(t, migratedToken.KeyPrefix, idempotent.KeyPrefix)

	inserted := model.Token{
		UserId:             8,
		Name:               "long-token",
		Key:                longKey,
		Status:             common.TokenStatusEnabled,
		CreatedTime:        1,
		AccessedTime:       1,
		ExpiredTime:        -1,
		RemainQuota:        200,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: false,
		ModelLimits:        "",
		AllowIps:           common.GetPointer(""),
		UsedQuota:          0,
		Group:              "default",
		CrossGroupRetry:    false,
	}
	if err := db.Create(&inserted).Error; err != nil {
		t.Fatalf("failed to insert long token after migration: %v", err)
	}

	var fetched model.Token
	if err := db.First(&fetched, "id = ?", inserted.Id).Error; err != nil {
		t.Fatalf("failed to fetch long token after migration: %v", err)
	}
	assert.Empty(t, fetched.Key)
	assert.Equal(t, model.HashTokenKey(longKey), fetched.KeyHash)
	assert.Equal(t, model.TokenKeyPrefix(longKey), fetched.KeyPrefix)
}

func TestTokenAutoMigrateUsesVarchar128KeyColumn(t *testing.T) {
	db := setupTokenControllerTestDB(t)

	if got := getTokenKeyColumnType(t, db, "sqlite"); got != "varchar(128)" {
		t.Fatalf("expected key column type varchar(128), got %q", got)
	}
}

func TestTokenMigrationFromChar48ToVarchar128(t *testing.T) {
	db := openTokenControllerTestDB(t)
	runTokenMigrationCompatibilityTest(t, db, "sqlite", nil)
}

func TestTokenMigrationFromChar48ToVarchar128MySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN to run mysql migration compatibility test")
	}

	db, managedTokensTable := openTokenControllerExternalDB(t, "mysql", dsn)
	runTokenMigrationCompatibilityTest(t, db, "mysql", managedTokensTable)
}

func TestTokenMigrationFromChar48ToVarchar128Postgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run postgres migration compatibility test")
	}

	db, managedTokensTable := openTokenControllerExternalDB(t, "postgres", dsn)
	runTokenMigrationCompatibilityTest(t, db, "postgres", managedTokensTable)
}

func TestGetAllTokensMasksKeyInResponse(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "list-token", "abcd1234efgh5678")
	seedToken(t, db, 2, "other-user-token", "zzzz1234yyyy5678")

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/?p=1&size=10", nil, 1)
	GetAllTokens(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected success response, got message: %s", response.Message)
	}

	var page tokenPageResponse
	if err := common.Unmarshal(response.Data, &page); err != nil {
		t.Fatalf("failed to decode token page response: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected exactly one token, got %d", len(page.Items))
	}
	if page.Items[0].Key != token.GetMaskedKey() {
		t.Fatalf("expected masked key %q, got %q", token.GetMaskedKey(), page.Items[0].Key)
	}
	if strings.Contains(recorder.Body.String(), token.Key) {
		t.Fatalf("list response leaked raw token key: %s", recorder.Body.String())
	}
}

func TestSearchTokensMasksKeyInResponse(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	rawKey := strings.Repeat("search-token-", 4)
	token := seedToken(t, db, 1, "searchable-token", rawKey)

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/search?token=sk-"+rawKey+"&p=1&size=10", nil, 1)
	SearchTokens(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected success response, got message: %s", response.Message)
	}

	var page tokenPageResponse
	if err := common.Unmarshal(response.Data, &page); err != nil {
		t.Fatalf("failed to decode search response: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected exactly one search result, got %d", len(page.Items))
	}
	if page.Items[0].Key != token.GetMaskedKey() {
		t.Fatalf("expected masked search key %q, got %q", token.GetMaskedKey(), page.Items[0].Key)
	}
	if strings.Contains(recorder.Body.String(), token.Key) {
		t.Fatalf("search response leaked raw token key: %s", recorder.Body.String())
	}

	prefixCtx, prefixRecorder := newAuthenticatedContext(
		t,
		http.MethodGet,
		"/api/token/search?token="+model.TokenKeyPrefix(rawKey)+"%25&p=1&size=10",
		nil,
		1,
	)
	SearchTokens(prefixCtx)
	prefixResponse := decodeAPIResponse(t, prefixRecorder)
	require.True(t, prefixResponse.Success, prefixResponse.Message)
	require.NoError(t, common.Unmarshal(prefixResponse.Data, &page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, token.Id, page.Items[0].ID)
	assert.NotContains(t, prefixRecorder.Body.String(), rawKey)
}

func TestGetTokenMasksKeyInResponse(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "detail-token", "qrst1234uvwx5678")

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/"+strconv.Itoa(token.Id), nil, 1)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	GetToken(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected success response, got message: %s", response.Message)
	}

	var detail tokenResponseItem
	if err := common.Unmarshal(response.Data, &detail); err != nil {
		t.Fatalf("failed to decode token detail response: %v", err)
	}
	if detail.Key != token.GetMaskedKey() {
		t.Fatalf("expected masked detail key %q, got %q", token.GetMaskedKey(), detail.Key)
	}
	if strings.Contains(recorder.Body.String(), token.Key) {
		t.Fatalf("detail response leaked raw token key: %s", recorder.Body.String())
	}
}

func TestUpdateTokenMasksKeyInResponse(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	token := seedToken(t, db, 1, "editable-token", "yzab1234cdef5678")

	body := map[string]any{
		"id":                   token.Id,
		"name":                 "updated-token",
		"expired_time":         -1,
		"remain_quota":         100,
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"model_limits":         "",
		"group":                "default",
		"cross_group_retry":    false,
	}

	ctx, recorder := newAuthenticatedContext(t, http.MethodPut, "/api/token/", body, 1)
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	if !response.Success {
		t.Fatalf("expected success response, got message: %s", response.Message)
	}

	var detail tokenResponseItem
	if err := common.Unmarshal(response.Data, &detail); err != nil {
		t.Fatalf("failed to decode token update response: %v", err)
	}
	if detail.Key != token.GetMaskedKey() {
		t.Fatalf("expected masked update key %q, got %q", token.GetMaskedKey(), detail.Key)
	}
	if strings.Contains(recorder.Body.String(), token.Key) {
		t.Fatalf("update response leaked raw token key: %s", recorder.Body.String())
	}
}

func TestAddTokenReturnsCredentialOnlyAtCreationAndPersistsDigest(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	body := map[string]any{
		"name":                 "created-token",
		"expired_time":         -1,
		"remain_quota":         100,
		"unlimited_quota":      true,
		"model_limits_enabled": false,
		"model_limits":         "",
		"group":                "default",
	}
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/", body, 1)
	AddToken(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var credential tokenKeyResponse
	require.NoError(t, common.Unmarshal(response.Data, &credential))
	require.True(t, strings.HasPrefix(credential.Key, "sk-"))
	rawKey := strings.TrimPrefix(credential.Key, "sk-")
	require.NotEmpty(t, rawKey)
	assert.Equal(t, "created-token", credential.Token.Name)
	assert.Equal(t, model.MaskTokenKey(model.TokenKeyPrefix(rawKey)), credential.Token.Key)
	assert.NotContains(t, credential.Token.Key, rawKey)

	var storedKey string
	require.NoError(t, db.Raw("SELECT key FROM tokens WHERE id = ?", credential.Token.ID).Scan(&storedKey).Error)
	assert.Equal(t, model.HashTokenKey(rawKey), storedKey)
	assert.NotContains(t, storedKey, rawKey)

	listCtx, listRecorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/?p=1&size=10", nil, 1)
	GetAllTokens(listCtx)
	assert.NotContains(t, listRecorder.Body.String(), rawKey)
}

func TestRotateTokenRequiresProofInvalidatesOldKeyAndRevealsReplacementOnce(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	oldKey := strings.Repeat("old-token-", 6)[:48]
	token := seedToken(t, db, 1, "rotated-token", oldKey)

	missingProofCtx, missingProofRecorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/"+strconv.Itoa(token.Id)+"/rotate", nil, 1)
	missingProofCtx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	RotateToken(missingProofCtx)
	assert.Equal(t, http.StatusForbidden, missingProofRecorder.Code)
	assert.Contains(t, missingProofRecorder.Body.String(), "SECURITY_PROOF_INVALID")

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/"+strconv.Itoa(token.Id)+"/rotate", nil, 1)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	proof := addTokenKeyRotateProof(t, db, ctx, 1, TokenKeyRotateTarget(token.Id))
	RotateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var credential tokenKeyResponse
	require.NoError(t, common.Unmarshal(response.Data, &credential))
	require.True(t, strings.HasPrefix(credential.Key, "sk-"))
	replacementKey := strings.TrimPrefix(credential.Key, "sk-")
	assert.NotEqual(t, oldKey, replacementKey)
	assert.Equal(t, model.MaskTokenKey(model.TokenKeyPrefix(replacementKey)), credential.Token.Key)

	_, err := model.GetTokenByKey(oldKey, true)
	require.Error(t, err)
	rotated, err := model.GetTokenByKey(replacementKey, true)
	require.NoError(t, err)
	assert.Equal(t, token.Id, rotated.Id)

	replayCtx, replayRecorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/"+strconv.Itoa(token.Id)+"/rotate", nil, 1)
	replayCtx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	replayCtx.Set("session_id", ctx.GetString("session_id"))
	replayCtx.Set("auth_version", ctx.GetInt64("auth_version"))
	replayCtx.Set("session_version", ctx.GetInt64("session_version"))
	identity, _ := ctx.Get("auth_identity")
	replayCtx.Set("auth_identity", identity)
	replayCtx.Request.Header.Set("X-Security-Proof", proof)
	RotateToken(replayCtx)
	assert.Contains(t, replayRecorder.Body.String(), "SECURITY_PROOF_CONSUMED")
	assert.NotContains(t, replayRecorder.Body.String(), replacementKey)
}

func TestRotateTokenProofDoesNotAllowRotatingAnotherUsersToken(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	oldKey := strings.Repeat("owner-token-", 5)[:48]
	token := seedToken(t, db, 1, "owned-token", oldKey)
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/"+strconv.Itoa(token.Id)+"/rotate", nil, 2)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(token.Id)}}
	addTokenKeyRotateProof(t, db, ctx, 2, TokenKeyRotateTarget(token.Id))
	RotateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	assert.NotContains(t, recorder.Body.String(), oldKey)
	authenticated, err := model.GetTokenByKey(oldKey, true)
	require.NoError(t, err)
	assert.Equal(t, token.Id, authenticated.Id)
}

func TestInvalidatedLegacyTokenCannotBeEnabledUntilRotated(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	const weakKey = "weak-legacy-key"
	require.NoError(t, db.Exec(
		"INSERT INTO tokens (user_id, key, key_prefix, status, name, expired_time, remain_quota) VALUES (?, ?, '', ?, ?, -1, 100)",
		1,
		weakKey,
		common.TokenStatusEnabled,
		"invalidated-legacy-token",
	).Error)
	require.NoError(t, model.MigrateTokenKeys())

	var token model.Token
	require.NoError(t, db.First(&token, "name = ?", "invalidated-legacy-token").Error)
	require.Equal(t, common.TokenStatusDisabled, token.Status)

	ctx, recorder := newAuthenticatedContext(
		t,
		http.MethodPut,
		"/api/token/?status_only=1",
		map[string]any{"id": token.Id, "status": common.TokenStatusEnabled},
		1,
	)
	UpdateToken(ctx)

	response := decodeAPIResponse(t, recorder)
	assert.False(t, response.Success)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
}
