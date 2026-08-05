package model

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/geminitaskresult"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	taskProviderResultURIForTest = "https://video.example.test/provider-path-sentinel?" +
		"sig=signed-query-sentinel"
	taskProviderCredentialForTest = "selected-credential-sentinel"
)

func prepareTaskProviderResultURITest(t *testing.T) {
	t.Helper()

	require.NoError(t, DB.AutoMigrate(&Task{}))
	require.NoError(t, DB.Exec("DELETE FROM tasks").Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec("DELETE FROM tasks").Error)
	})
}

func rawTaskProviderResultURI(t *testing.T, taskID int64) sql.NullString {
	t.Helper()

	var stored sql.NullString
	require.NoError(t, DB.Table("tasks").
		Select("provider_result_uri").
		Where("id = ?", taskID).
		Row().
		Scan(&stored))
	return stored
}

func requireSafeTaskProviderResultURIError(
	t *testing.T,
	err error,
	forbidden ...string,
) {
	t.Helper()

	require.Error(t, err)
	for _, value := range forbidden {
		assert.NotContains(t, err.Error(), value)
	}
}

func TestTaskProviderResultURIEncryptsAtRest(t *testing.T) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	plaintextURI := taskProviderResultURIForTest +
		"&credential_marker=" + taskProviderCredentialForTest
	require.Contains(t, plaintextURI, taskProviderCredentialForTest)

	task := &Task{
		TaskID:   "task_provider_result_encrypted",
		Platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
		Data:     []byte(`{"done":true}`),
	}
	changed, err := task.SetProviderResultURI(plaintextURI)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, task.EncryptedProviderResultURI)
	insertTask(t, task)

	stored := rawTaskProviderResultURI(t, task.ID)
	require.True(t, stored.Valid)
	assert.True(t, common.IsDataEncryptionEnvelope(stored.String))
	for _, forbidden := range []string{
		plaintextURI,
		"provider-path-sentinel",
		"signed-query-sentinel",
		taskProviderCredentialForTest,
	} {
		assert.NotContains(t, stored.String, forbidden)
	}

	var loaded Task
	require.NoError(t, DB.First(&loaded, task.ID).Error)
	opened, err := loaded.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, plaintextURI, opened)

	var columns []struct {
		Name    string `gorm:"column:name"`
		Type    string `gorm:"column:type"`
		NotNull int    `gorm:"column:notnull"`
	}
	require.NoError(t, DB.Raw("PRAGMA table_info(tasks)").Scan(&columns).Error)
	var found bool
	for _, column := range columns {
		if !strings.EqualFold(column.Name, "provider_result_uri") {
			continue
		}
		found = true
		assert.Contains(t, strings.ToUpper(column.Type), "TEXT")
		assert.Zero(t, column.NotNull)
	}
	assert.True(t, found)
}

func TestTaskProviderResultURIRequiresKeyringEvenInPreparationMode(t *testing.T) {
	configureModelDataEncryption(t, "", "", "false")
	task := &Task{}

	changed, err := task.SetProviderResultURI(taskProviderResultURIForTest)

	assert.False(t, changed)
	assert.Nil(t, task.EncryptedProviderResultURI)
	requireSafeTaskProviderResultURIError(
		t,
		err,
		taskProviderResultURIForTest,
		"provider-path-sentinel",
		"signed-query-sentinel",
		taskProviderCredentialForTest,
	)
}

func TestTaskProviderResultURIRuntimeReadFailsClosed(t *testing.T) {
	t.Run("plaintext", func(t *testing.T) {
		configureModelDataEncryption(
			t,
			"k1="+modelTestDataEncryptionKey('a'),
			"k1",
			"true",
		)
		stored := taskProviderResultURIForTest
		task := &Task{EncryptedProviderResultURI: &stored}

		opened, err := task.OpenProviderResultURI()

		assert.Empty(t, opened)
		requireSafeTaskProviderResultURIError(t, err, stored)
	})

	t.Run("plaintext in preparation mode", func(t *testing.T) {
		configureModelDataEncryption(t, "", "", "false")
		stored := taskProviderResultURIForTest
		task := &Task{EncryptedProviderResultURI: &stored}

		opened, err := task.OpenProviderResultURI()

		assert.Empty(t, opened)
		requireSafeTaskProviderResultURIError(t, err, stored)
	})

	t.Run("wrong domain", func(t *testing.T) {
		configureModelDataEncryption(
			t,
			"k1="+modelTestDataEncryptionKey('a'),
			"k1",
			"true",
		)
		stored, err := common.SealDataEncryptionValueRequired(
			"tasks:wrong_provider_result_domain",
			taskProviderResultURIForTest,
		)
		require.NoError(t, err)
		task := &Task{EncryptedProviderResultURI: &stored}

		opened, err := task.OpenProviderResultURI()

		assert.Empty(t, opened)
		requireSafeTaskProviderResultURIError(
			t,
			err,
			stored,
			taskProviderResultURIForTest,
		)
	})

	t.Run("corrupt envelope", func(t *testing.T) {
		configureModelDataEncryption(
			t,
			"k1="+modelTestDataEncryptionKey('a'),
			"k1",
			"true",
		)
		stored := "naenc:v1:k1:corrupt-wrapped-key-sentinel:corrupt-payload-sentinel"
		task := &Task{EncryptedProviderResultURI: &stored}

		opened, err := task.OpenProviderResultURI()

		assert.Empty(t, opened)
		requireSafeTaskProviderResultURIError(
			t,
			err,
			stored,
			"corrupt-wrapped-key-sentinel",
			"corrupt-payload-sentinel",
		)
	})

	t.Run("oversized encrypted plaintext", func(t *testing.T) {
		configureModelDataEncryption(
			t,
			"k1="+modelTestDataEncryptionKey('a'),
			"k1",
			"true",
		)
		const sentinel = "oversized-encrypted-provider-result-sentinel"
		uri := "https://video.example.test/" +
			strings.Repeat("x", geminitaskresult.MaxProviderResultURIBytes) +
			sentinel
		stored, err := common.SealDataEncryptionValueRequired(
			taskProviderResultURIDomain,
			uri,
		)
		require.NoError(t, err)
		task := &Task{EncryptedProviderResultURI: &stored}

		opened, err := task.OpenProviderResultURI()

		assert.Empty(t, opened)
		requireSafeTaskProviderResultURIError(t, err, uri, sentinel)
		assert.EqualError(
			t,
			err,
			"open task provider result URI: plaintext exceeds "+
				strconv.Itoa(geminitaskresult.MaxProviderResultURIBytes)+" bytes",
		)
	})

	t.Run("unknown root key", func(t *testing.T) {
		configureModelDataEncryption(
			t,
			"k1="+modelTestDataEncryptionKey('a'),
			"k1",
			"true",
		)
		stored, err := common.SealDataEncryptionValueRequired(
			taskProviderResultURIDomain,
			taskProviderResultURIForTest,
		)
		require.NoError(t, err)
		configureModelDataEncryption(
			t,
			"k2="+modelTestDataEncryptionKey('b'),
			"k2",
			"true",
		)
		task := &Task{EncryptedProviderResultURI: &stored}

		opened, err := task.OpenProviderResultURI()

		assert.Empty(t, opened)
		requireSafeTaskProviderResultURIError(
			t,
			err,
			stored,
			taskProviderResultURIForTest,
		)
	})
}

func TestTaskProviderResultURISetIsIdempotent(t *testing.T) {
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	task := &Task{}

	changed, err := task.SetProviderResultURI(taskProviderResultURIForTest)
	require.NoError(t, err)
	require.True(t, changed)
	firstPointer := task.EncryptedProviderResultURI
	require.NotNil(t, firstPointer)
	firstEnvelope := *firstPointer

	changed, err = task.SetProviderResultURI(taskProviderResultURIForTest)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Same(t, firstPointer, task.EncryptedProviderResultURI)
	assert.Equal(t, firstEnvelope, *task.EncryptedProviderResultURI)

	const changedURI = "https://video.example.test/changed-provider-result?sig=second"
	changed, err = task.SetProviderResultURI(changedURI)
	require.NoError(t, err)
	assert.True(t, changed)
	require.NotNil(t, task.EncryptedProviderResultURI)
	assert.NotSame(t, firstPointer, task.EncryptedProviderResultURI)
	assert.NotEqual(t, firstEnvelope, *task.EncryptedProviderResultURI)

	opened, err := task.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Equal(t, changedURI, opened)
}

func TestTaskProviderResultURIIsExcludedFromJSON(t *testing.T) {
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	task := &Task{TaskID: "task_provider_result_json"}
	changed, err := task.SetProviderResultURI(taskProviderResultURIForTest)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, task.EncryptedProviderResultURI)

	data, err := common.Marshal(task)
	require.NoError(t, err)
	serialized := string(data)
	assert.NotContains(t, serialized, "provider_result_uri")
	assert.NotContains(t, serialized, *task.EncryptedProviderResultURI)
	assert.NotContains(t, serialized, taskProviderResultURIForTest)
	assert.NotContains(t, serialized, "provider-path-sentinel")
	assert.NotContains(t, serialized, "signed-query-sentinel")
}

func TestTaskProviderResultURIClearRestoresNull(t *testing.T) {
	prepareTaskProviderResultURITest(t)
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	task := &Task{
		TaskID: "task_provider_result_clear",
		Data:   []byte(`{"done":false}`),
	}
	changed, err := task.SetProviderResultURI(taskProviderResultURIForTest)
	require.NoError(t, err)
	require.True(t, changed)
	insertTask(t, task)
	require.True(t, rawTaskProviderResultURI(t, task.ID).Valid)

	changed, err = task.SetProviderResultURI("")
	require.NoError(t, err)
	assert.True(t, changed)
	task.ClearProviderResultURI()
	assert.Nil(t, task.EncryptedProviderResultURI)
	require.NoError(t, task.Update())

	stored := rawTaskProviderResultURI(t, task.ID)
	assert.False(t, stored.Valid)
	var loaded Task
	require.NoError(t, DB.First(&loaded, task.ID).Error)
	opened, err := loaded.OpenProviderResultURI()
	require.NoError(t, err)
	assert.Empty(t, opened)
}

func TestTaskSnapshotIncludesEncryptedProviderResultURI(t *testing.T) {
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	task := &Task{Status: TaskStatusInProgress, Data: []byte(`{"done":false}`)}
	changed, err := task.SetProviderResultURI(taskProviderResultURIForTest)
	require.NoError(t, err)
	require.True(t, changed)

	firstSnapshot := task.Snapshot()
	require.NotNil(t, task.EncryptedProviderResultURI)
	firstEnvelope := *task.EncryptedProviderResultURI
	assert.Equal(t, firstEnvelope, firstSnapshot.EncryptedProviderResultURI)

	*task.EncryptedProviderResultURI = "mutated-after-snapshot"
	assert.Equal(t, firstEnvelope, firstSnapshot.EncryptedProviderResultURI)
	assert.False(t, firstSnapshot.Equal(task.Snapshot()))
}

func TestTaskIsGeminiTaskRecognizesCurrentAndLegacyPlatforms(t *testing.T) {
	tests := []struct {
		name     string
		platform constant.TaskPlatform
		expected bool
	}{
		{
			name:     "current numeric platform",
			platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGemini)),
			expected: true,
		},
		{name: "legacy lowercase", platform: "gemini", expected: true},
		{name: "legacy title case", platform: "Gemini", expected: true},
		{name: "legacy uppercase", platform: "GEMINI", expected: true},
		{
			name:     "vertex numeric platform",
			platform: constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeVertexAi)),
			expected: false,
		},
		{name: "unrelated", platform: "suno", expected: false},
		{name: "empty", platform: "", expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := &Task{Platform: test.platform}
			assert.Equal(t, test.expected, task.IsGeminiTask())
		})
	}
}

func TestTaskProviderResultURIRejectsOversizedValue(t *testing.T) {
	configureModelDataEncryption(
		t,
		"k1="+modelTestDataEncryptionKey('a'),
		"k1",
		"true",
	)
	const sentinel = "oversized-provider-result-sentinel"
	uri := "https://video.example.test/" +
		strings.Repeat("x", geminitaskresult.MaxProviderResultURIBytes) +
		sentinel
	task := &Task{}

	changed, err := task.SetProviderResultURI(uri)

	assert.False(t, changed)
	assert.Nil(t, task.EncryptedProviderResultURI)
	requireSafeTaskProviderResultURIError(t, err, uri, sentinel)
}
