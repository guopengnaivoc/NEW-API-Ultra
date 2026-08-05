package operation_setting

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func preserveToolPrices(t *testing.T) {
	t.Helper()
	original, err := config.ConfigToMap(getToolPriceSetting())
	require.NoError(t, err)
	t.Cleanup(func() {
		updated, updateErr := config.GlobalConfig.Update("tool_price_setting", original)
		require.NoError(t, updateErr)
		require.True(t, updated)
		RebuildToolPriceIndex()
	})
}

func TestToolPriceHardcodedFallbacksSurviveMissingOperatorConfig(t *testing.T) {
	preserveToolPrices(t)
	LoadToolPricesFromJSONString(`{}`)

	expectedDefaults := map[string]float64{
		"web_search":         10,
		"web_search_preview": 10,
		"file_search":        2.5,
		"google_search":      14,
		"image_generation":   150,
	}
	for name, expected := range expectedDefaults {
		assert.Equal(t, expected, GetToolPrice(name), name)
	}
	assert.Equal(t, 25.0, GetToolPriceForModel("web_search_preview", "gpt-4o-2024-11-20"))
	assert.Equal(t, 25.0, GetToolPriceForModel("web_search_preview", "gpt-4.1-mini"))
}

func TestToolPriceOperatorOverridePrecedenceAndExplicitZero(t *testing.T) {
	preserveToolPrices(t)
	LoadToolPricesFromJSONString(`{
		"image_generation": 0,
		"web_search": 12,
		"web_search_preview": 0,
		"web_search_preview:gpt-4o*": 30,
		"web_search_preview:gpt-4o-mini*": 0,
		"web_search_preview:custom-model*": 7
	}`)

	assert.Equal(t, 0.0, GetToolPrice("image_generation"))
	assert.Equal(t, 12.0, GetToolPrice("web_search"))
	assert.Equal(t, 0.0, GetToolPriceForModel("web_search_preview", "o1"))
	assert.Equal(t, 30.0, GetToolPriceForModel("web_search_preview", "gpt-4o"))
	assert.Equal(t, 0.0, GetToolPriceForModel("web_search_preview", "gpt-4o-mini"))
	assert.Equal(t, 25.0, GetToolPriceForModel("web_search_preview", "gpt-4.1"))
	assert.Equal(t, 7.0, GetToolPriceForModel("web_search_preview", "custom-model-v2"))

	DeleteToolPriceForTest("web_search_preview:gpt-4o*")
	assert.Equal(t, 25.0, GetToolPriceForModel("web_search_preview", "gpt-4o"))

	DeleteToolPriceForTest("web_search")
	assert.Equal(t, 10.0, GetToolPrice("web_search"))
}

func TestToolPriceCustomFunctionHasNoHardcodedFallback(t *testing.T) {
	preserveToolPrices(t)
	LoadToolPricesFromJSONString(`{}`)
	emptySnapshot := getToolPriceSetting()

	assert.Equal(t, 0.0, GetToolPrice("lookup_customer"))

	SetToolPriceForTest("lookup_customer", 5)
	pricedSnapshot := getToolPriceSetting()
	assert.NotSame(t, emptySnapshot, pricedSnapshot)
	assert.NotContains(t, emptySnapshot.Prices, "lookup_customer")
	assert.Equal(t, 5.0, pricedSnapshot.Prices["lookup_customer"])
	assert.Equal(t, 5.0, GetToolPrice("lookup_customer"))

	SetToolPriceForTest("lookup_customer", 0)
	zeroSnapshot := getToolPriceSetting()
	assert.NotSame(t, pricedSnapshot, zeroSnapshot)
	assert.Equal(t, 5.0, pricedSnapshot.Prices["lookup_customer"])
	assert.Equal(t, 0.0, zeroSnapshot.Prices["lookup_customer"])
	assert.Equal(t, 0.0, GetToolPrice("lookup_customer"))

	DeleteToolPriceForTest("lookup_customer")
	deletedSnapshot := getToolPriceSetting()
	assert.NotSame(t, zeroSnapshot, deletedSnapshot)
	assert.NotContains(t, deletedSnapshot.Prices, "lookup_customer")
}

func TestValidateToolPricesJSON(t *testing.T) {
	valid := []string{
		`{}`,
		`{"web_search":0}`,
		`{"web_search":10,"custom_fn":2.5}`,
	}
	for _, value := range valid {
		assert.NoError(t, ValidateToolPricesJSON(value), value)
	}

	invalid := []string{
		`null`,
		`[]`,
		`{"web_search":null}`,
		`{"web_search":true}`,
		`{"web_search":"0"}`,
		`{"web_search":-1}`,
		`{"web_search":1e999}`,
		`{"web_search":`,
	}
	for _, value := range invalid {
		assert.Error(t, ValidateToolPricesJSON(value), value)
	}
}

func TestLoadToolPricesFromJSONStringReplacesMapAndKeepsValidSiblings(t *testing.T) {
	preserveToolPrices(t)
	LoadToolPricesFromJSONString(`{"before":1}`)
	before := getToolPriceSetting()

	LoadToolPricesFromJSONString(`{
		"web_search": 0,
		"custom_fn": 3,
		"file_search": null,
		"google_search": -1,
		"image_generation": "0"
	}`)

	after := getToolPriceSetting()
	assert.NotSame(t, before, after)
	assert.Equal(t, map[string]float64{"before": 1}, before.Prices)
	require.Len(t, after.Prices, 2)
	assert.Equal(t, 0.0, after.Prices["web_search"])
	assert.Equal(t, 3.0, after.Prices["custom_fn"])
	assert.Equal(t, 0.0, GetToolPrice("web_search"))
	assert.Equal(t, 3.0, GetToolPrice("custom_fn"))
	assert.Equal(t, 2.5, GetToolPrice("file_search"))
	assert.Equal(t, 14.0, GetToolPrice("google_search"))
	assert.Equal(t, 150.0, GetToolPrice("image_generation"))

	LoadToolPricesFromJSONString(`{"image_generation":0}`)
	replaced := getToolPriceSetting()
	assert.NotSame(t, after, replaced)
	require.Len(t, replaced.Prices, 1)
	assert.NotContains(t, replaced.Prices, "web_search")
	assert.NotContains(t, replaced.Prices, "custom_fn")
	assert.Equal(t, map[string]float64{"web_search": 0, "custom_fn": 3}, after.Prices)
	assert.Equal(t, 10.0, GetToolPrice("web_search"))
	assert.Equal(t, 0.0, GetToolPrice("custom_fn"))
	assert.Equal(t, 0.0, GetToolPrice("image_generation"))
}

func TestToolPriceTestHelperDoesNotExposeInvalidValuesToIndex(t *testing.T) {
	preserveToolPrices(t)
	LoadToolPricesFromJSONString(`{}`)
	SetToolPriceForTest("web_search", -1)
	SetToolPriceForTest("file_search", math.Inf(1))
	SetToolPriceForTest("image_generation", math.NaN())
	SetToolPriceForTest("custom_fn", math.NaN())

	assert.Equal(t, 10.0, GetToolPrice("web_search"))
	assert.Equal(t, 2.5, GetToolPrice("file_search"))
	assert.Equal(t, 150.0, GetToolPrice("image_generation"))
	assert.Equal(t, 0.0, GetToolPrice("custom_fn"))
}
