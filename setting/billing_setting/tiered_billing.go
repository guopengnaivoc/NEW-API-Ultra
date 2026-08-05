package billing_setting

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
	BillingModeField      = "billing_mode"
	BillingExprField      = "billing_expr"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	BillingMode map[string]string `json:"billing_mode"`
	BillingExpr map[string]string `json:"billing_expr"`
}

var billingSetting = BillingSetting{
	BillingMode: make(map[string]string),
	BillingExpr: make(map[string]string),
}

// PersistedExpressionRejection identifies a legacy model expression that was
// excluded from the runtime snapshot during database synchronization.
type PersistedExpressionRejection struct {
	Model string
	Err   error
}

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

func (s *BillingSetting) Validate() error {
	models := make([]string, 0, len(s.BillingExpr))
	for model := range s.BillingExpr {
		models = append(models, model)
	}
	sort.Strings(models)

	for _, model := range models {
		expression := s.BillingExpr[model]
		if strings.TrimSpace(expression) == "" {
			continue
		}
		if err := smokeTestExpr(expression); err != nil {
			return fmt.Errorf("invalid billing expression for model %q: %w", model, err)
		}
	}
	return nil
}

// PublishPersistedConfig publishes the database snapshot while quarantining
// legacy expressions that no longer satisfy the current billing safety rules.
// Interactive updates continue to use ConfigManager.Update directly and reject
// the whole submitted map before it is persisted.
func PublishPersistedConfig(values map[string]string) (map[string]string, []PersistedExpressionRejection, error) {
	current := getBillingSetting()
	modes := lo.Assign(current.BillingMode)
	expressions := lo.Assign(current.BillingExpr)

	if value, ok := values[BillingModeField]; ok {
		modes = make(map[string]string)
		if err := common.UnmarshalJsonStr(value, &modes); err != nil {
			return nil, nil, fmt.Errorf("decode persisted billing modes: %w", err)
		}
		if modes == nil {
			modes = make(map[string]string)
		}
	}
	if value, ok := values[BillingExprField]; ok {
		expressions = make(map[string]string)
		if err := common.UnmarshalJsonStr(value, &expressions); err != nil {
			return nil, nil, fmt.Errorf("decode persisted billing expressions: %w", err)
		}
		if expressions == nil {
			expressions = make(map[string]string)
		}
	}

	models := make([]string, 0, len(expressions))
	for model := range expressions {
		models = append(models, model)
	}
	sort.Strings(models)

	rejections := make([]PersistedExpressionRejection, 0)
	for _, model := range models {
		expression := expressions[model]
		if strings.TrimSpace(expression) == "" {
			continue
		}
		if err := smokeTestExpr(expression); err != nil {
			rejections = append(rejections, PersistedExpressionRejection{
				Model: model,
				Err:   err,
			})
			delete(expressions, model)
			delete(modes, model)
		}
	}

	modeJSON, err := common.Marshal(modes)
	if err != nil {
		return nil, nil, fmt.Errorf("encode persisted billing modes: %w", err)
	}
	expressionJSON, err := common.Marshal(expressions)
	if err != nil {
		return nil, nil, fmt.Errorf("encode persisted billing expressions: %w", err)
	}
	published := map[string]string{
		BillingModeField: string(modeJSON),
		BillingExprField: string(expressionJSON),
	}
	updated, err := config.GlobalConfig.Update("billing_setting", published)
	if err != nil {
		return nil, nil, err
	}
	if !updated {
		return nil, nil, errors.New("billing configuration is not registered")
	}
	return published, rejections, nil
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func getBillingSetting() *BillingSetting {
	return config.Snapshot[BillingSetting]("billing_setting")
}

func GetBillingMode(model string) string {
	current := getBillingSetting()
	if mode, ok := current.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

func GetBillingExpr(model string) (string, bool) {
	current := getBillingSetting()
	expr, ok := current.BillingExpr[model]
	return expr, ok
}

func GetBillingModeCopy() map[string]string {
	return lo.Assign(getBillingSetting().BillingMode)
}

func GetBillingExprCopy() map[string]string {
	return lo.Assign(getBillingSetting().BillingExpr)
}

func GetPricingSyncData(base map[string]any) map[string]any {
	extra := make(map[string]any, 2)
	if modes := GetBillingModeCopy(); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := GetBillingExprCopy(); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	return lo.Assign(base, extra)
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{},
		{P: 1},
		{C: 1},
		{Len: 1},
		{CR: 1},
		{CC: 1},
		{CC1h: 1},
		{Img: 1},
		{ImgO: 1},
		{AI: 1},
		{AO: 1},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 200000, C: 200000, Len: 200000},
		{P: 1000000, C: 1000000, Len: 1000000},
		{
			P: 1000, C: 1000, Len: 1000,
			CR: 1000, CC: 1000, CC1h: 1000,
			Img: 1000, ImgO: 1000, AI: 1000, AO: 1000,
		},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			_, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("token vector %+v: run failed: %w", v, err)
			}
		}
	}
	return nil
}
