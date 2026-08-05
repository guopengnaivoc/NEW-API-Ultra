package billingexpr_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunExprRejectsInvalidBillingResult(t *testing.T) {
	tests := []struct {
		name       string
		expression string
	}{
		{name: "negative", expression: "-1.0"},
		{name: "nan", expression: "0.0 / 0.0"},
		{name: "positive infinity", expression: "1.0 / 0.0"},
		{name: "negative infinity", expression: "-1.0 / 0.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cost, _, err := billingexpr.RunExpr(test.expression, billingexpr.TokenParams{})

			require.Error(t, err)
			assert.Zero(t, cost)
		})
	}
}

func TestComputeTieredQuotaRejectsInvalidBillingResult(t *testing.T) {
	tests := []struct {
		name       string
		expression string
	}{
		{name: "negative", expression: "-1.0"},
		{name: "nan", expression: "0.0 / 0.0"},
		{name: "positive infinity", expression: "1.0 / 0.0"},
		{name: "negative infinity", expression: "-1.0 / 0.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := &billingexpr.BillingSnapshot{
				BillingMode:  "tiered_expr",
				ExprString:   test.expression,
				ExprHash:     billingexpr.ExprHashString(test.expression),
				GroupRatio:   1,
				QuotaPerUnit: 500_000,
			}

			result, err := billingexpr.ComputeTieredQuota(snapshot, billingexpr.TokenParams{})

			require.Error(t, err)
			assert.Zero(t, result.ActualQuotaAfterGroup)
		})
	}
}
