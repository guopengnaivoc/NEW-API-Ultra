package model

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createRestoreFixtureToken(t *testing.T, key string, mutate func(*Token)) Token {
	t.Helper()
	token := Token{
		UserId:         1,
		Key:            key,
		Name:           key,
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    0,
		UsedQuota:      500,
		UnlimitedQuota: false,
	}
	if mutate != nil {
		mutate(&token)
	}
	require.NoError(t, DB.Create(&token).Error)
	return token
}

func tokenStatusOf(t *testing.T, id int) int {
	t.Helper()
	var current Token
	require.NoError(t, DB.First(&current, "id = ?", id).Error)
	return current.Status
}

// A token exhausted by ValidateUserToken must become usable again once a refund
// gives it positive quota; otherwise every refunded token is permanently dead.
func TestExhaustedTokenIsRestoredByRefundAndAuthenticatesAgain(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-refund-token", nil)

	_, err := ValidateUserToken(token.Key)
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.Equal(t, common.TokenStatusExhausted, tokenStatusOf(t, token.Id))

	require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 300))

	assert.Equal(t, common.TokenStatusEnabled, tokenStatusOf(t, token.Id))

	validated, err := ValidateUserToken(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.Id, validated.Id)
	assert.Equal(t, 300, validated.RemainQuota)
}

// The restore is conditional: only an exhausted-and-now-funded token qualifies.
func TestRefundNeverRevivesIneligibleTokens(t *testing.T) {
	cases := []struct {
		name           string
		key            string
		mutate         func(*Token)
		refund         int
		expectedStatus int
	}{
		{
			name:           "disabled token stays disabled",
			key:            "restore-disabled-token",
			mutate:         func(tk *Token) { tk.Status = common.TokenStatusDisabled },
			refund:         300,
			expectedStatus: common.TokenStatusDisabled,
		},
		{
			name: "expired token stays expired",
			key:  "restore-expired-token",
			mutate: func(tk *Token) {
				tk.Status = common.TokenStatusExhausted
				tk.ExpiredTime = common.GetTimestamp() - 10
			},
			refund:         300,
			expectedStatus: common.TokenStatusExhausted,
		},
		{
			name: "invalidated key stays exhausted",
			key:  "restore-invalidated-token",
			mutate: func(tk *Token) {
				tk.Status = common.TokenStatusExhausted
			},
			refund:         300,
			expectedStatus: common.TokenStatusExhausted,
		},
		{
			name: "zero refund leaves quota at zero",
			key:  "restore-zero-refund-token",
			mutate: func(tk *Token) {
				tk.Status = common.TokenStatusExhausted
			},
			refund:         0,
			expectedStatus: common.TokenStatusExhausted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			useUserCacheMiniRedis(t)

			token := createRestoreFixtureToken(t, tc.key, tc.mutate)
			if tc.name == "invalidated key stays exhausted" {
				require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).
					Update("key_prefix", invalidTokenKeyPrefix).Error)
			}

			require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, tc.refund))

			assert.Equal(t, tc.expectedStatus, tokenStatusOf(t, token.Id))
		})
	}
}

// A negative amount is rejected by IncreaseTokenQuota before any write, so it
// can neither change quota nor revive the token.
func TestNegativeRefundIsRejectedAndRevivesNothing(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-negative-refund-token", func(tk *Token) {
		tk.Status = common.TokenStatusExhausted
	})

	require.Error(t, IncreaseTokenQuota(token.Id, token.Key, -100))

	var current Token
	require.NoError(t, DB.First(&current, "id = ?", token.Id).Error)
	assert.Equal(t, common.TokenStatusExhausted, current.Status)
	assert.Equal(t, 0, current.RemainQuota)
}

// A soft-deleted token must never be revived, even when a stale refund credits
// its row. The restore predicate checks deleted_at explicitly because callers
// inside billing transactions run on Unscoped sessions.
func TestRefundNeverRevivesSoftDeletedToken(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-deleted-token", func(tk *Token) {
		tk.Status = common.TokenStatusExhausted
	})
	require.NoError(t, DB.Delete(&Token{}, "id = ?", token.Id).Error)

	// Credit the deleted row the way an in-flight settlement transaction would,
	// then attempt the restore directly.
	require.NoError(t, DB.Unscoped().Model(&Token{}).Where("id = ?", token.Id).
		Update("remain_quota", 300).Error)
	require.NoError(t, RestoreExhaustedTokenIfFunded(DB.Unscoped(), token.Id))

	var current Token
	require.NoError(t, DB.Unscoped().First(&current, "id = ?", token.Id).Error)
	assert.Equal(t, common.TokenStatusExhausted, current.Status)
	assert.True(t, current.DeletedAt.Valid)
}

// Redis stores only the token id, so the database remains authoritative for
// status: a cached exhausted token must authenticate again after a refund,
// and a cached funded token must not survive being drained.
func TestRefundRestoreIsVisibleThroughRedisCachedLookup(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-cached-token", nil)

	_, err := ValidateUserToken(token.Key)
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.Equal(t, common.TokenStatusExhausted, tokenStatusOf(t, token.Id))

	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	require.Equal(t, token.Id, cached.Id)

	require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 250))

	restored, err := ValidateUserToken(token.Key)
	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusEnabled, restored.Status)

	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).
		Update("remain_quota", 0).Error)
	_, err = ValidateUserToken(token.Key)
	assert.ErrorIs(t, err, ErrTokenInvalid)
	assert.Equal(t, common.TokenStatusExhausted, tokenStatusOf(t, token.Id))
}

// Repeated validation of a healthy restored token must not keep rewriting it.
func TestRepeatedValidationAfterRestoreDoesNotAmplifyWrites(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-write-amplification-token", nil)
	_, err := ValidateUserToken(token.Key)
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 400))

	var updates int
	const callbackName = "test:count_token_updates"
	require.NoError(t, DB.Callback().Update().After("gorm:update").Register(callbackName, func(d *gorm.DB) {
		if d.Statement != nil && d.Statement.Table == "tokens" {
			updates++
		}
	}))
	t.Cleanup(func() {
		_ = DB.Callback().Update().Remove(callbackName)
	})

	for i := 0; i < 5; i++ {
		validated, err := ValidateUserToken(token.Key)
		require.NoError(t, err)
		require.Equal(t, common.TokenStatusEnabled, validated.Status)
	}

	assert.Zero(t, updates)
}

// Concurrent refunds must converge on a single enabled token, and a refund
// racing with re-exhaustion must never leave an enabled zero-quota token.
func TestConcurrentRefundRestoreConvergesToConsistentStatus(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-concurrent-token", nil)
	_, err := ValidateUserToken(token.Key)
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.Equal(t, common.TokenStatusExhausted, tokenStatusOf(t, token.Id))

	const refunds = 8
	var wg sync.WaitGroup
	errs := make([]error, refunds)
	wg.Add(refunds)
	for i := 0; i < refunds; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = IncreaseTokenQuota(token.Id, token.Key, 10)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}

	var final Token
	require.NoError(t, DB.First(&final, "id = ?", token.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, final.Status)
	assert.Equal(t, refunds*10, final.RemainQuota)

	// Draining the restored token must re-exhaust it rather than leave an
	// enabled token with no quota.
	require.NoError(t, DecreaseTokenQuota(token.Id, token.Key, final.RemainQuota))
	_, err = ValidateUserToken(token.Key)
	assert.ErrorIs(t, err, ErrTokenInvalid)
	assert.Equal(t, common.TokenStatusExhausted, tokenStatusOf(t, token.Id))
}

// The exact interleaving from the review verdict, made deterministic with the
// persist-time hook (no sleeps, no probabilistic scheduling):
//
//  1. authentication reads a stale snapshot (Enabled, RemainQuota=0);
//  2. before the status write lands, a refund commits RemainQuota=10;
//  3. the delayed authentication write then runs against the moved database.
//
// The stale write must not clobber the refund: the final row must be
// (RemainQuota=10, Status=Enabled) and the token must authenticate.
func TestStaleExhaustedWriteDoesNotClobberConcurrentRefund(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-stale-write-token", nil)

	fired := false
	testHookBeforeTokenStatusPersist = func(tokenId int, status int) {
		if fired {
			return
		}
		fired = true
		require.Equal(t, token.Id, tokenId)
		require.Equal(t, common.TokenStatusExhausted, status)
		// The refund lands after the snapshot read but before the status write.
		require.NoError(t, IncreaseTokenQuota(token.Id, token.Key, 10))
	}
	t.Cleanup(func() { testHookBeforeTokenStatusPersist = nil })

	validated, err := ValidateUserToken(token.Key)
	require.True(t, fired, "interleaving hook must have run")

	var final Token
	require.NoError(t, DB.First(&final, "id = ?", token.Id).Error)
	assert.Equal(t, 10, final.RemainQuota)
	assert.Equal(t, common.TokenStatusEnabled, final.Status)

	// The delayed authentication itself must already see the refunded token.
	require.NoError(t, err)
	require.NotNil(t, validated)
	assert.Equal(t, common.TokenStatusEnabled, validated.Status)
	assert.Equal(t, 10, validated.RemainQuota)

	// And a fresh authentication succeeds.
	again, err := ValidateUserToken(token.Key)
	require.NoError(t, err)
	assert.Equal(t, token.Id, again.Id)
}

// Same interleaving for the Expired branch: extending expired_time between the
// snapshot read and the status write must win over the stale Expired verdict.
func TestStaleExpiredWriteDoesNotClobberConcurrentExtension(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-stale-expired-token", func(tk *Token) {
		tk.ExpiredTime = common.GetTimestamp() - 10
		tk.RemainQuota = 100
	})

	fired := false
	testHookBeforeTokenStatusPersist = func(tokenId int, status int) {
		if fired {
			return
		}
		fired = true
		require.Equal(t, common.TokenStatusExpired, status)
		require.NoError(t, DB.Model(&Token{}).Where("id = ?", tokenId).
			Update("expired_time", common.GetTimestamp()+3600).Error)
	}
	t.Cleanup(func() { testHookBeforeTokenStatusPersist = nil })

	validated, err := ValidateUserToken(token.Key)
	require.True(t, fired)

	var final Token
	require.NoError(t, DB.First(&final, "id = ?", token.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, final.Status)

	require.NoError(t, err)
	assert.Equal(t, common.TokenStatusEnabled, validated.Status)
}

// RotateTokenKey's contract: once rotation commits, the old key is invalid
// immediately. That must hold even in the interleaving where a concurrent
// refund defeats the exhausted guard of a stale authentication:
//
//  1. authentication reads a stale snapshot of oldKey (Enabled, RemainQuota=0);
//  2. RotateTokenKey commits replacementKey;
//  3. a refund commits RemainQuota=10;
//  4. the delayed exhausted guard UPDATE misses because quota is positive.
//
// The stale request presented a credential that no longer exists, so it must
// fail closed. It must not be handed the rotated row, and no returned Token
// may pair the old plaintext key with the replacement key's hash.
func TestRotatedKeyStaysInvalidWhenRefundDefeatsExhaustedGuard(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	const oldKey = "rotation-stale-old-key"
	const replacementKey = "rotation-stale-replacement-key"
	replacementHash := HashTokenKey(replacementKey)
	token := createRestoreFixtureToken(t, oldKey, nil)

	fired := false
	testHookBeforeTokenStatusPersist = func(tokenId int, status int) {
		if fired {
			return
		}
		fired = true
		require.Equal(t, token.Id, tokenId)
		require.Equal(t, common.TokenStatusExhausted, status)
		// Rotation and refund both commit between the snapshot read and the
		// delayed status write.
		_, err := RotateTokenKey(token.Id, token.UserId, replacementKey)
		require.NoError(t, err)
		require.NoError(t, IncreaseTokenQuota(token.Id, oldKey, 10))
	}
	t.Cleanup(func() { testHookBeforeTokenStatusPersist = nil })

	stale, err := ValidateUserToken(oldKey)
	require.True(t, fired, "interleaving hook must have run")
	require.Error(t, err, "a rotated key must never authenticate")
	if stale != nil {
		assert.False(t, stale.Key == oldKey && stale.KeyHash == replacementHash,
			"stale plaintext key must never be paired with the replacement credential")
	}

	// The rotated row itself must be untouched by the stale request: the
	// replacement hash, the refunded quota, and the Enabled status all stand.
	var final Token
	require.NoError(t, DB.First(&final, "id = ?", token.Id).Error)
	assert.Equal(t, replacementHash, final.KeyHash)
	assert.Equal(t, common.TokenStatusEnabled, final.Status)
	assert.Equal(t, 10, final.RemainQuota)

	// The replacement key authenticates with the refunded quota.
	rotated, err := ValidateUserToken(replacementKey)
	require.NoError(t, err)
	assert.Equal(t, token.Id, rotated.Id)
	assert.Equal(t, common.TokenStatusEnabled, rotated.Status)
	assert.Equal(t, 10, rotated.RemainQuota)
	assert.Equal(t, replacementHash, rotated.KeyHash)

	// The old key stays dead on a fresh attempt as well.
	_, err = ValidateUserToken(oldKey)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

// When nothing intervenes, the conditional write must still land Exhausted
// atomically — the guard misses only when the database actually moved.
func TestConditionalExhaustedWriteStillLandsWhenQuotaIsGone(t *testing.T) {
	truncateTables(t)
	useUserCacheMiniRedis(t)

	token := createRestoreFixtureToken(t, "restore-still-exhausts-token", nil)

	validated, err := ValidateUserToken(token.Key)
	require.ErrorIs(t, err, ErrTokenInvalid)
	assert.Equal(t, common.TokenStatusExhausted, validated.Status)
	assert.Equal(t, common.TokenStatusExhausted, tokenStatusOf(t, token.Id))
}
