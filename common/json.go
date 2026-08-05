package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"unicode/utf8"
)

func Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func UnmarshalJsonStr(data string, v any) error {
	return json.Unmarshal(StringToByteSlice(data), v)
}

func DecodeJson(reader io.Reader, v any) error {
	return json.NewDecoder(reader).Decode(v)
}

func DecodeJsonDocument(reader io.Reader, v any) error {
	decoder := json.NewDecoder(reader)
	return decodeJSONDocument(decoder, v)
}

func decodeJSONDocument(decoder *json.Decoder, v any) error {
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("JSON document contains multiple values")
	}
	return err
}

// JSONSemanticEqual compares two complete JSON documents by decoded structure.
// Object key order and whitespace are ignored, while array order, scalar types,
// string contents, and number tokens remain significant.
func JSONSemanticEqual(left []byte, right []byte) (bool, error) {
	if !utf8.Valid(left) {
		return false, errors.New("decode left JSON document: invalid UTF-8")
	}
	var leftValue any
	leftDecoder := json.NewDecoder(bytes.NewReader(left))
	leftDecoder.UseNumber()
	if err := decodeJSONDocument(leftDecoder, &leftValue); err != nil {
		return false, fmt.Errorf("decode left JSON document: %w", err)
	}

	if !utf8.Valid(right) {
		return false, errors.New("decode right JSON document: invalid UTF-8")
	}
	var rightValue any
	rightDecoder := json.NewDecoder(bytes.NewReader(right))
	rightDecoder.UseNumber()
	if err := decodeJSONDocument(rightDecoder, &rightValue); err != nil {
		return false, fmt.Errorf("decode right JSON document: %w", err)
	}

	return reflect.DeepEqual(leftValue, rightValue), nil
}

func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func GetJsonType(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "unknown"
	}
	firstChar := trimmed[0]
	switch firstChar {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

// JsonRawMessageToString returns JSON strings as their decoded value and other JSON values as raw text.
func JsonRawMessageToString(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] != '"' {
		return string(trimmed)
	}
	var value string
	if err := Unmarshal(trimmed, &value); err != nil {
		return string(trimmed)
	}
	return value
}
