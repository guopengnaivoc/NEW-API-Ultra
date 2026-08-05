package model

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func restoreGrokSnapshot(t *testing.T) {
	t.Helper()
	current := config.Snapshot[model_setting.GrokSettings]("grok")
	require.NotNil(t, current)
	enabled := strconv.FormatBool(current.ViolationDeductionEnabled)
	amount := strconv.FormatFloat(current.ViolationDeductionAmount, 'f', -1, 64)
	t.Cleanup(func() {
		updated, err := config.GlobalConfig.Update("grok", map[string]string{
			"violation_deduction_enabled": enabled,
			"violation_deduction_amount":  amount,
		})
		require.NoError(t, err)
		require.True(t, updated)
	})
}

func TestHandleConfigUpdatePublishesSnapshot(t *testing.T) {
	restoreGrokSnapshot(t)
	before := config.Snapshot[model_setting.GrokSettings]("grok")

	handled, err := handleConfigUpdate("grok.violation_deduction_amount", "0.25")

	require.NoError(t, err)
	require.True(t, handled)
	after := config.Snapshot[model_setting.GrokSettings]("grok")
	assert.NotSame(t, before, after)
	assert.Equal(t, 0.25, after.ViolationDeductionAmount)
	assert.NotEqual(t, 0.25, before.ViolationDeductionAmount)
}

func TestHandleConfigUpdateLeavesUnknownModuleForLegacyDispatch(t *testing.T) {
	handled, err := handleConfigUpdate("missing.value", "ignored")

	require.NoError(t, err)
	assert.False(t, handled)
}

func TestValidateOptionValueRejectsNonFiniteLayeredFloat(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
		t.Run(value, func(t *testing.T) {
			require.Error(t, validateOptionValue("grok.violation_deduction_amount", value))
		})
	}
}

func TestUpdateOptionDoesNotPersistNonFiniteLayeredFloat(t *testing.T) {
	db := useFrontendOptionMigrationDB(t)

	err := UpdateOption("grok.violation_deduction_amount", "NaN")

	require.Error(t, err)
	requireOptionMissing(t, db, "grok.violation_deduction_amount")
}

func preserveBillingOptionState(t *testing.T) {
	t.Helper()
	values, err := config.ConfigToMap(config.Snapshot[billing_setting.BillingSetting]("billing_setting"))
	require.NoError(t, err)

	keys := []string{
		"billing_setting.billing_mode",
		"billing_setting.billing_expr",
	}
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	previousValues := make(map[string]string, len(keys))
	existed := make(map[string]bool, len(keys))
	for _, key := range keys {
		previousValues[key], existed[key] = common.OptionMap[key]
	}
	common.OptionMapRWMutex.Unlock()

	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("billing_setting", values)
		require.NoError(t, updateErr)
		require.True(t, updated)

		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		for _, key := range keys {
			if existed[key] {
				common.OptionMap[key] = previousValues[key]
			} else {
				delete(common.OptionMap, key)
			}
		}
	})
}

func TestUpdateOptionDoesNotPersistInvalidBillingExpression(t *testing.T) {
	preserveBillingOptionState(t)
	db := useFrontendOptionMigrationDB(t)
	const key = "billing_setting.billing_expr"
	const validValue = `{"model":"p * 2"}`
	require.NoError(t, updateOptionMap(key, validValue))

	err := UpdateOption(key, `{"model":"0.0 / 0.0"}`)

	require.Error(t, err)
	requireOptionMissing(t, db, key)
	expression, ok := billing_setting.GetBillingExpr("model")
	require.True(t, ok)
	assert.Equal(t, "p * 2", expression)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, validValue, common.OptionMap[key])
	common.OptionMapRWMutex.RUnlock()
}

func TestPersistedInvalidBillingExpressionDoesNotReplaceRuntimeState(t *testing.T) {
	preserveBillingOptionState(t)
	const key = "billing_setting.billing_expr"
	const validValue = `{"model":"p * 2"}`
	require.NoError(t, updateOptionMap(key, validValue))

	err := updateOptionMap(key, `{"model":"cr > 0 ? -1.0 : 1.0"}`)

	require.Error(t, err)
	expression, ok := billing_setting.GetBillingExpr("model")
	require.True(t, ok)
	assert.Equal(t, "p * 2", expression)
	common.OptionMapRWMutex.RLock()
	assert.Equal(t, validValue, common.OptionMap[key])
	common.OptionMapRWMutex.RUnlock()
}

func TestLoadOptionsQuarantinesInvalidBillingExpressionPerModel(t *testing.T) {
	preserveBillingOptionState(t)
	db := useFrontendOptionMigrationDB(t)
	updated, err := config.GlobalConfig.Update("billing_setting", map[string]string{
		billing_setting.BillingModeField: `{}`,
		billing_setting.BillingExprField: `{}`,
	})
	require.NoError(t, err)
	require.True(t, updated)

	const modeValue = `{"good":"tiered_expr","legacy":"tiered_expr"}`
	const expressionValue = `{"good":"p * 2","legacy":"cr > 0 ? -1.0 : 1.0"}`
	require.NoError(t, db.Create(&[]Option{
		{Key: "billing_setting.billing_mode", Value: modeValue},
		{Key: "billing_setting.billing_expr", Value: expressionValue},
	}).Error)

	loadOptionsFromDatabase()
	loadOptionsFromDatabase()

	assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode("good"))
	expression, ok := billing_setting.GetBillingExpr("good")
	require.True(t, ok)
	assert.Equal(t, "p * 2", expression)
	assert.Equal(t, billing_setting.BillingModeRatio, billing_setting.GetBillingMode("legacy"))
	_, ok = billing_setting.GetBillingExpr("legacy")
	assert.False(t, ok)

	common.OptionMapRWMutex.RLock()
	publishedMode := common.OptionMap["billing_setting.billing_mode"]
	publishedExpression := common.OptionMap["billing_setting.billing_expr"]
	common.OptionMapRWMutex.RUnlock()
	assert.JSONEq(t, `{"good":"tiered_expr"}`, publishedMode)
	assert.JSONEq(t, `{"good":"p * 2"}`, publishedExpression)

	assert.JSONEq(t, modeValue, requireOptionValue(t, db, "billing_setting.billing_mode"))
	assert.JSONEq(t, expressionValue, requireOptionValue(t, db, "billing_setting.billing_expr"))
}
