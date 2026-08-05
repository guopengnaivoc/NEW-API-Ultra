package common

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJsonRawMessageToString(t *testing.T) {
	tests := []struct {
		name string
		data json.RawMessage
		want string
	}{
		{
			name: "object",
			data: json.RawMessage(`{"city":"Paris","days":0,"strict":false}`),
			want: `{"city":"Paris","days":0,"strict":false}`,
		},
		{
			name: "string",
			data: json.RawMessage(`"{\"city\":\"Paris\",\"days\":0,\"strict\":false}"`),
			want: `{"city":"Paris","days":0,"strict":false}`,
		},
		{
			name: "null",
			data: json.RawMessage(`null`),
			want: "",
		},
		{
			name: "empty",
			data: nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, JsonRawMessageToString(tt.data))
		})
	}
}

func TestDecodeJsonDocument(t *testing.T) {
	tests := []struct {
		name     string
		document string
		wantErr  bool
	}{
		{
			name:     "single object with trailing whitespace",
			document: "{\"value\":\"accepted\"} \n\t",
		},
		{
			name:     "second JSON value",
			document: `{"value":"accepted"} {}`,
			wantErr:  true,
		},
		{
			name:     "malformed suffix",
			document: `{"value":"accepted"} garbage`,
			wantErr:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var decoded struct {
				Value string `json:"value"`
			}

			err := DecodeJsonDocument(strings.NewReader(test.document), &decoded)

			if test.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "accepted", decoded.Value)
		})
	}
}

func TestJSONSemanticEqual(t *testing.T) {
	tests := []struct {
		name      string
		left      string
		right     string
		wantEqual bool
		wantErr   bool
	}{
		{
			name:      "object key order and whitespace are insignificant",
			left:      `{"title":"first","count":2}`,
			right:     " {\n  \"count\": 2,\n  \"title\": \"first\"\n} ",
			wantEqual: true,
		},
		{
			name:  "byte multiset collision preserves key value associations",
			left:  `{"a":1,"b":2}`,
			right: `{"a":2,"b":1}`,
		},
		{
			name:  "array order remains significant",
			left:  `["a","b"]`,
			right: `["b","a"]`,
		},
		{
			name:  "string character position remains significant",
			left:  `{"clip":"ab"}`,
			right: `{"clip":"ba"}`,
		},
		{
			name:  "large integers retain exact distinction",
			left:  `{"value":9007199254740992}`,
			right: `{"value":9007199254740993}`,
		},
		{
			name:  "alternate number spellings are conservatively different",
			left:  `{"value":1}`,
			right: `{"value":1.0}`,
		},
		{
			name:    "malformed left document is rejected",
			left:    `{"value":`,
			right:   `{"value":1}`,
			wantErr: true,
		},
		{
			name:    "trailing right value is rejected",
			left:    `{"value":1}`,
			right:   `{"value":1} []`,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			equal, err := JSONSemanticEqual([]byte(test.left), []byte(test.right))

			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.wantEqual, equal)
		})
	}
}

func TestJSONSemanticEqualRejectsInvalidUTF8(t *testing.T) {
	invalidUTF8 := []byte{'"', 'h', 'e', 'l', 'l', 'o', 0xff, '"'}
	validReplacement := []byte(`"hello\uFFFD"`)
	tests := []struct {
		name    string
		left    []byte
		right   []byte
		wantErr string
	}{
		{
			name:    "left operand",
			left:    invalidUTF8,
			right:   validReplacement,
			wantErr: "decode left JSON document: invalid UTF-8",
		},
		{
			name:    "right operand",
			left:    validReplacement,
			right:   invalidUTF8,
			wantErr: "decode right JSON document: invalid UTF-8",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			equal, err := JSONSemanticEqual(test.left, test.right)

			assert.False(t, equal)
			require.EqualError(t, err, test.wantErr)
			assert.NotContains(t, err.Error(), "hello")
		})
	}
}
