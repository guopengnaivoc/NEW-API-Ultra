package service

import (
	"image"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRouterCacheCreateTokensRejectsNonFiniteDerivation(t *testing.T) {
	usage := dto.Usage{
		PromptTokens: 100,
		Cost:         math.Inf(1),
	}
	priceData := types.PriceData{
		ModelRatio:         1,
		CacheCreationRatio: 2,
	}

	assert.Equal(t, -1, CalcOpenRouterCacheCreateTokens(usage, priceData))
}

func TestViolationFeeQuotaPreservesRoundingAndRejectsOverflow(t *testing.T) {
	quota, err := calcViolationFeeQuota(1.25, 1)
	require.NoError(t, err)
	assert.Equal(t, common.QuotaRound(1.25*common.QuotaPerUnit), quota)

	quota, err = calcViolationFeeQuota(float64(common.MaxQuota), 2)
	assert.Zero(t, quota)
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	assert.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}

func patchImageMeta(width, height int) *relaytypes.FileMeta {
	source := relaytypes.NewBase64FileSource("", "image/png")
	cached := relaytypes.NewMemoryCachedData("", "image/png", 0)
	cached.ImageConfig = &image.Config{Width: width, Height: height}
	cached.ImageFormat = "png"
	source.SetCache(cached)
	return relaytypes.NewImageFileMeta(source, "high")
}

func TestImageTokenPatchRoundingPreservesProviderValues(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	originalGetMediaToken := constant.GetMediaToken
	originalGetMediaTokenNotStream := constant.GetMediaTokenNotStream
	t.Cleanup(func() {
		constant.GetMediaToken = originalGetMediaToken
		constant.GetMediaTokenNotStream = originalGetMediaTokenNotStream
	})
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true

	belowCapTokens, err := getImageToken(
		ctx,
		patchImageMeta(32, 32),
		"gpt-4.1-nano",
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, 2, belowCapTokens)

	cappedTokens, err := getImageToken(
		ctx,
		patchImageMeta(2048, 2048),
		"gpt-4.1-nano",
		true,
	)
	require.NoError(t, err)
	// The square image is adjusted to 39x39 whole patches before the
	// provider multiplier is applied, so it uses 1,521 patches.
	assert.Equal(t, common.QuotaRound(1521*2.46), cappedTokens)
}
