package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionBalanceQuotaPreservesCeilingAndRejectsOverflow(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
	})
	common.QuotaPerUnit = 10

	quota, err := calcSubscriptionBalanceQuota(1.01)
	require.NoError(t, err)
	assert.Equal(t, 11, quota)

	quota, err = calcSubscriptionBalanceQuota(float64(common.MaxQuota))
	assert.Zero(t, quota)
	var clamp *common.QuotaClamp
	require.ErrorAs(t, err, &clamp)
	assert.Equal(t, common.QuotaClampOverflow, clamp.Kind)
}
