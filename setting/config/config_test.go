package config

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testConfigWithMap struct {
	Modes map[string]string `json:"modes"`
	Exprs map[string]string `json:"exprs"`
	Name  string            `json:"name"`
}

type testConfigWithFloat struct {
	Value float64 `json:"value"`
}

type testConfigWithMixedFields struct {
	Name  string            `json:"name"`
	Modes map[string]string `json:"modes"`
	Ratio float64           `json:"ratio"`
}

func TestUpdateConfigFromMap_MapReplacement(t *testing.T) {
	cfg := &testConfigWithMap{
		Modes: map[string]string{
			"model-a": "tiered_expr",
			"model-b": "tiered_expr",
		},
		Exprs: map[string]string{
			"model-a": "p * 5 + c * 25",
			"model-b": "p * 10 + c * 50",
		},
		Name: "billing",
	}

	// Simulate removing model-a: new value only has model-b
	err := UpdateConfigFromMap(cfg, map[string]string{
		"modes": `{"model-b": "tiered_expr"}`,
		"exprs": `{"model-b": "p * 10 + c * 50"}`,
	})
	require.NoError(t, err)
	assert.NotContains(t, cfg.Modes, "model-a")
	assert.NotContains(t, cfg.Exprs, "model-a")
	assert.Equal(t, "tiered_expr", cfg.Modes["model-b"])
	assert.Equal(t, "p * 10 + c * 50", cfg.Exprs["model-b"])
}

func TestUpdateConfigFromMap_EmptyMapClearsAll(t *testing.T) {
	cfg := &testConfigWithMap{
		Modes: map[string]string{
			"model-a": "tiered_expr",
		},
		Exprs: map[string]string{
			"model-a": "p * 5 + c * 25",
		},
	}

	err := UpdateConfigFromMap(cfg, map[string]string{
		"modes": `{}`,
		"exprs": `{}`,
	})
	require.NoError(t, err)
	assert.Empty(t, cfg.Modes)
	assert.Empty(t, cfg.Exprs)
}

func TestUpdateConfigFromMap_ScalarFieldsUnchanged(t *testing.T) {
	cfg := &testConfigWithMap{
		Modes: map[string]string{"m": "v"},
		Name:  "old",
	}

	err := UpdateConfigFromMap(cfg, map[string]string{
		"name": "new",
	})
	require.NoError(t, err)
	assert.Equal(t, "new", cfg.Name)
	assert.Equal(t, "v", cfg.Modes["m"])
}

func TestConfigManagerConcurrentReadUpdate(t *testing.T) {
	manager := NewConfigManager()
	manager.Register("billing", &testConfigWithMap{
		Name:  "one",
		Modes: map[string]string{"model-one": "ratio"},
		Exprs: map[string]string{"model-one": "p"},
	})

	const iterations = 64
	start := make(chan struct{})
	invalidSnapshot := make(chan struct{}, 1)
	updateErr := make(chan error, 1)

	var workers sync.WaitGroup
	workers.Add(2)

	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < iterations; i++ {
			options := map[string]string{
				"billing.name":  "one",
				"billing.modes": `{"model-one":"ratio"}`,
				"billing.exprs": `{"model-one":"p"}`,
			}
			if i%2 == 1 {
				options = map[string]string{
					"billing.name":  "two",
					"billing.modes": `{"model-two":"tiered_expr"}`,
					"billing.exprs": `{"model-two":"p * 2"}`,
				}
			}
			if err := manager.LoadFromDB(options); err != nil {
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
			snapshot := manager.Get("billing").(*testConfigWithMap)
			isOne := snapshot.Name == "one" &&
				len(snapshot.Modes) == 1 &&
				snapshot.Modes["model-one"] == "ratio" &&
				len(snapshot.Exprs) == 1 &&
				snapshot.Exprs["model-one"] == "p"
			isTwo := snapshot.Name == "two" &&
				len(snapshot.Modes) == 1 &&
				snapshot.Modes["model-two"] == "tiered_expr" &&
				len(snapshot.Exprs) == 1 &&
				snapshot.Exprs["model-two"] == "p * 2"
			if !isOne && !isTwo {
				select {
				case invalidSnapshot <- struct{}{}:
				default:
				}
			}
		}
	}()

	close(start)
	workers.Wait()

	require.Empty(t, updateErr)
	assert.Empty(t, invalidSnapshot, "reader observed fields from different config versions")
}

func TestConfigManagerUpdatePublishesDetachedSnapshot(t *testing.T) {
	manager := NewConfigManager()
	initial := &testConfigWithMap{
		Modes: map[string]string{"old": "ratio"},
		Exprs: map[string]string{"old": "p"},
		Name:  "before",
	}
	manager.Register("billing", initial)

	before := manager.Get("billing").(*testConfigWithMap)
	updated, err := manager.Update("billing", map[string]string{
		"modes": `{"new":"tiered_expr"}`,
		"exprs": `{"new":"p * 2"}`,
		"name":  "after",
	})

	require.NoError(t, err)
	require.True(t, updated)
	after := manager.Get("billing").(*testConfigWithMap)
	assert.NotSame(t, before, after)
	assert.Equal(t, "before", before.Name)
	assert.Equal(t, map[string]string{"old": "ratio"}, before.Modes)
	assert.Equal(t, map[string]string{"old": "p"}, before.Exprs)
	assert.Equal(t, "after", after.Name)
	assert.Equal(t, map[string]string{"new": "tiered_expr"}, after.Modes)
	assert.Equal(t, map[string]string{"new": "p * 2"}, after.Exprs)
}

func TestConfigManagerUpdateUsesLatestSnapshot(t *testing.T) {
	manager := NewConfigManager()
	manager.Register("billing", &testConfigWithMap{
		Modes: map[string]string{"old": "ratio"},
		Exprs: map[string]string{"old": "p"},
		Name:  "before",
	})

	updated, err := manager.Update("missing", map[string]string{"name": "ignored"})
	require.NoError(t, err)
	assert.False(t, updated)

	updated, err = manager.Update("billing", map[string]string{"name": "after"})
	require.NoError(t, err)
	require.True(t, updated)
	updated, err = manager.Update("billing", map[string]string{
		"modes": `{"new":"tiered_expr"}`,
	})
	require.NoError(t, err)
	require.True(t, updated)

	current := manager.Get("billing").(*testConfigWithMap)
	assert.Equal(t, "after", current.Name)
	assert.Equal(t, map[string]string{"new": "tiered_expr"}, current.Modes)
	assert.Equal(t, map[string]string{"old": "p"}, current.Exprs)
}

func TestConfigManagerUpdateRejectsNonFiniteFloat(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
		t.Run(value, func(t *testing.T) {
			manager := NewConfigManager()
			manager.Register("float", &testConfigWithFloat{Value: 1.5})
			before := manager.Get("float").(*testConfigWithFloat)

			updated, err := manager.Update("float", map[string]string{"value": value})

			require.True(t, updated)
			require.Error(t, err)
			after := manager.Get("float").(*testConfigWithFloat)
			assert.Same(t, before, after)
			assert.Equal(t, 1.5, after.Value)
		})
	}
}

func TestConfigManagerUpdateAcceptsFiniteFloat(t *testing.T) {
	tests := []struct {
		value string
		want  float64
	}{
		{value: "1.25", want: 1.25},
		{value: "0", want: 0},
		{value: "-2.5", want: -2.5},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			manager := NewConfigManager()
			manager.Register("float", &testConfigWithFloat{Value: 1.5})

			updated, err := manager.Update("float", map[string]string{"value": test.value})

			require.NoError(t, err)
			require.True(t, updated)
			assert.Equal(t, test.want, manager.Get("float").(*testConfigWithFloat).Value)
		})
	}
}

func TestConfigManagerValidateDoesNotPublish(t *testing.T) {
	manager := NewConfigManager()
	manager.Register("float", &testConfigWithFloat{Value: 1.5})
	before := manager.Get("float").(*testConfigWithFloat)

	recognized, err := manager.Validate("float", map[string]string{"value": "2.5"})

	require.NoError(t, err)
	require.True(t, recognized)
	after := manager.Get("float").(*testConfigWithFloat)
	assert.Same(t, before, after)
	assert.Equal(t, 1.5, after.Value)
}

func TestConfigManagerLoadFromDBSkipsRejectedFieldAndPublishesValidFields(t *testing.T) {
	manager := NewConfigManager()
	manager.Register("mixed", &testConfigWithMixedFields{
		Name:  "seed",
		Modes: map[string]string{"old": "ratio"},
		Ratio: 0.8,
	})

	err := manager.LoadFromDB(map[string]string{
		"mixed.name":  "from-db",
		"mixed.modes": `{"new":"tiered_expr"}`,
		"mixed.ratio": "NaN",
	})

	require.NoError(t, err)
	current := manager.Get("mixed").(*testConfigWithMixedFields)
	assert.Equal(t, "from-db", current.Name)
	assert.Equal(t, map[string]string{"new": "tiered_expr"}, current.Modes)
	assert.Equal(t, 0.8, current.Ratio)
}

func TestSnapshotFailsFastForRegistrationMistakes(t *testing.T) {
	previous := GlobalConfig
	GlobalConfig = NewConfigManager()
	t.Cleanup(func() {
		GlobalConfig = previous
	})

	GlobalConfig.Register("float", &testConfigWithFloat{Value: 1.5})
	assert.Equal(t, 1.5, Snapshot[testConfigWithFloat]("float").Value)

	require.PanicsWithValue(
		t,
		`config snapshot "missing" has type <nil>, want *config.testConfigWithFloat`,
		func() {
			Snapshot[testConfigWithFloat]("missing")
		},
	)
	require.PanicsWithValue(
		t,
		`config snapshot "float" has type *config.testConfigWithFloat, want *config.testConfigWithMap`,
		func() {
			Snapshot[testConfigWithMap]("float")
		},
	)
}
