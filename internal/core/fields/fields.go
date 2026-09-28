// Package fields reads fields out of an API's JSON the same way in every model.
//
// APIs leave fields out, send null, and now and then send another type than they
// document. Every model reads its fields through these, so a missing or odd value becomes
// a plain default (an empty string, an empty map or slice, false, a zero time) rather than
// a panic, or the text "<nil>" in a table.
package fields

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// Object is a decoded JSON object.
type Object = map[string]any

// Text is data[key] as text: empty when it is missing or null, else its text, so a 0 is
// "0" and a false "false" rather than nothing.
func Text(data Object, key string) string {
	return String(data[key])
}

// String is a JSON value as text, as Text reads a field.
func String(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

// Map is value when it is a JSON object, else an empty one.
func Map(value any) Object {
	if found, ok := value.(map[string]any); ok {
		return found
	}
	return Object{}
}

// Items is value when it is a JSON array, else an empty one.
func Items(value any) []any {
	if found, ok := value.([]any); ok {
		return found
	}
	return nil
}

// Objects is the JSON objects in value, when it is an array; anything else in it is
// left out.
func Objects(value any) []Object {
	var found []Object
	for _, item := range Items(value) {
		if object, ok := item.(map[string]any); ok {
			found = append(found, object)
		}
	}
	return found
}

// Strings is the strings in value, when it is an array.
func Strings(value any) []string {
	var found []string
	for _, item := range Items(value) {
		if text, ok := item.(string); ok {
			found = append(found, text)
		}
	}
	return found
}

// Flag is value when it is true or false; ok is false when it is missing, null or
// anything else.
func Flag(value any) (flag bool, ok bool) {
	flag, ok = value.(bool)
	return flag, ok
}

// Bool is value when it is true or false, else false.
func Bool(value any) bool {
	flag, _ := Flag(value)
	return flag
}

// Number is value as a number: a JSON number, or a string of digits, as some token
// endpoints send expires_in. A boolean is not one.
func Number(value any) (number float64, ok bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	case string:
		text := strings.TrimSpace(v)
		if text == "" || strings.Trim(text, "0123456789") != "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(text, 64)
		return parsed, err == nil
	}
	return 0, false
}

// Int is value as a whole number, or 0.
func Int(value any) int {
	number, _ := Number(value)
	return int(number)
}

// When is data[key] as a UTC time, or the zero time when it is missing, unreadable or
// one of Microsoft's "not known" dates (0001-01-01).
func When(data Object, key string) time.Time {
	return util.ParseDatetime(data[key])
}
