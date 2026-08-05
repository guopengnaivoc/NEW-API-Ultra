package billing_setting

import (
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func billingMapJSON(t *testing.T, value map[string]string) string {
	t.Helper()
	data, err := common.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func restoreBillingSnapshot(t *testing.T) {
	t.Helper()
	current := config.Snapshot[BillingSetting]("billing_setting")
	require.NotNil(t, current)
	modeJSON := billingMapJSON(t, current.BillingMode)
	exprJSON := billingMapJSON(t, current.BillingExpr)
	t.Cleanup(func() {
		updated, err := config.GlobalConfig.Update("billing_setting", map[string]string{
			BillingModeField: modeJSON,
			BillingExprField: exprJSON,
		})
		require.NoError(t, err)
		require.True(t, updated)
	})
}

func TestBillingAccessorsReadPublishedSnapshot(t *testing.T) {
	restoreBillingSnapshot(t)
	updated, err := config.GlobalConfig.Update("billing_setting", map[string]string{
		BillingModeField: `{"model":"tiered_expr"}`,
		BillingExprField: `{"model":"p * 2"}`,
	})
	require.NoError(t, err)
	require.True(t, updated)

	assert.Equal(t, BillingModeTieredExpr, GetBillingMode("model"))
	expr, ok := GetBillingExpr("model")
	assert.True(t, ok)
	assert.Equal(t, "p * 2", expr)

	modeCopy := GetBillingModeCopy()
	exprCopy := GetBillingExprCopy()
	modeCopy["model"] = BillingModeRatio
	exprCopy["model"] = "changed"
	assert.Equal(t, BillingModeTieredExpr, GetBillingMode("model"))
	expr, ok = GetBillingExpr("model")
	assert.True(t, ok)
	assert.Equal(t, "p * 2", expr)
}

func TestBillingSnapshotsSupportConcurrentReadAndPublish(t *testing.T) {
	restoreBillingSnapshot(t)
	const iterations = 64
	start := make(chan struct{})
	updateErr := make(chan error, 1)

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < iterations; i++ {
			values := map[string]string{
				BillingModeField: `{"model-one":"ratio"}`,
				BillingExprField: `{"model-one":"p"}`,
			}
			if i%2 == 1 {
				values = map[string]string{
					BillingModeField: `{"model-two":"tiered_expr"}`,
					BillingExprField: `{"model-two":"p * 2"}`,
				}
			}
			if _, err := config.GlobalConfig.Update("billing_setting", values); err != nil {
				select {
				case updateErr <- err:
				default:
				}
				return
			}
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < iterations; i++ {
			_ = GetBillingMode("model-one")
			_, _ = GetBillingExpr("model-one")
			modeCopy := GetBillingModeCopy()
			exprCopy := GetBillingExprCopy()
			modeCopy["detached"] = BillingModeRatio
			exprCopy["detached"] = "p"
		}
	}()

	close(start)
	workers.Wait()

	require.Empty(t, updateErr)
	assert.NotContains(t, config.Snapshot[BillingSetting]("billing_setting").BillingMode, "detached")
	assert.NotContains(t, config.Snapshot[BillingSetting]("billing_setting").BillingExpr, "detached")
}

func TestSmokeTestExprRejectsNonFiniteResults(t *testing.T) {
	tests := []struct {
		name       string
		expression string
	}{
		{name: "nan", expression: "0.0 / 0.0"},
		{name: "positive infinity", expression: "1.0 / 0.0"},
		{name: "negative infinity", expression: "-1.0 / 0.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Error(t, SmokeTestExpr(test.expression))
		})
	}
}

func TestSmokeTestExprExercisesEveryTokenDimension(t *testing.T) {
	for _, variable := range []string{
		"p",
		"c",
		"len",
		"cr",
		"cc",
		"cc1h",
		"img",
		"img_o",
		"ai",
		"ao",
	} {
		t.Run(variable, func(t *testing.T) {
			expression := fmt.Sprintf("%s > 0 ? -1.0 : 0.0", variable)
			assert.Error(t, SmokeTestExpr(expression))
		})
	}
}

func TestBillingConfigRejectsInvalidExpressionBeforePublishing(t *testing.T) {
	restoreBillingSnapshot(t)
	updated, err := config.GlobalConfig.Update("billing_setting", map[string]string{
		BillingExprField: `{"model":"p * 2"}`,
	})
	require.NoError(t, err)
	require.True(t, updated)
	before := config.Snapshot[BillingSetting]("billing_setting")

	updated, err = config.GlobalConfig.Update("billing_setting", map[string]string{
		BillingExprField: `{"model":"0.0 / 0.0"}`,
	})

	require.True(t, updated)
	require.Error(t, err)
	after := config.Snapshot[BillingSetting]("billing_setting")
	assert.Same(t, before, after)
	assert.Equal(t, "p * 2", after.BillingExpr["model"])
}

func TestBillingConfigAllowsBlankInactiveExpressionAlongsideValidModel(t *testing.T) {
	restoreBillingSnapshot(t)

	updated, err := config.GlobalConfig.Update("billing_setting", map[string]string{
		BillingExprField: `{"active":"p * 2","inactive":"   "}`,
	})

	require.NoError(t, err)
	require.True(t, updated)
	current := config.Snapshot[BillingSetting]("billing_setting")
	assert.Equal(t, "p * 2", current.BillingExpr["active"])
	assert.Equal(t, "   ", current.BillingExpr["inactive"])
}
