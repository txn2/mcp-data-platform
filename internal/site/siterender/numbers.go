package siterender

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"

	"github.com/osteele/liquid/values"
)

// The engine's arithmetic filters compute an integer result as an int64, and
// everything that reads an integer back reads an int and refuses an int64: a
// range bound, and a loop's limit, offset and cols (#2633). So
// {% assign n = 2 | plus: 1 %}{% for i in (1..n) %} failed with "can't convert
// int64(3) to type int". These are the same filters with an integer result
// that is an int. They are otherwise the engine's own, down to what each does
// with a float, a string and a divisor it does not take.

var errDivisionByZero = errors.New("division by zero")

var float64Type = reflect.TypeFor[float64]()

// addArithmetic replaces the arithmetic filters whose integer results are
// int64.
func (e *engine) addArithmetic() {
	e.cfg.AddFilter("plus", arithmetic(func(a, b int64) int64 { return a + b }, func(a, b float64) float64 { return a + b }))
	e.cfg.AddFilter("minus", arithmetic(func(a, b int64) int64 { return a - b }, func(a, b float64) float64 { return a - b }))
	e.cfg.AddFilter("times", arithmetic(func(a, b int64) int64 { return a * b }, func(a, b float64) float64 { return a * b }))
	e.cfg.AddFilter("at_least", arithmetic(func(a, b int64) int64 { return max(a, b) }, func(a, b float64) float64 { return max(a, b) }))
	e.cfg.AddFilter("at_most", arithmetic(func(a, b int64) int64 { return min(a, b) }, func(a, b float64) float64 { return min(a, b) }))
	e.cfg.AddFilter("divided_by", dividedBy)
	e.cfg.AddFilter("modulo", modulo)
	e.cfg.AddFilter("abs", absolute)
}

// arithmetic is a filter that does integer arithmetic on two integers and
// float arithmetic otherwise, as the engine's does.
func arithmetic(ints func(a, b int64) int64, floats func(a, b float64) float64) func(a, b any) any {
	return func(a, b any) any {
		if isInteger(a) && isInteger(b) {
			return int(ints(toInt64(a), toInt64(b)))
		}
		return floats(toFloat64(a), toFloat64(b))
	}
}

// dividedBy is integer division by an integer divisor and float division by a
// float one. The dividend is read as a float first, as the engine reads it.
func dividedBy(a float64, b any) (any, error) {
	switch q := b.(type) {
	case int, int8, int16, int32, int64, uint8, uint16, uint32:
		d := toInt64(q)
		if d == 0 {
			return nil, errDivisionByZero
		}
		return int(int64(a) / d), nil
	case float32, float64:
		d := toFloat64(q)
		if d == 0 {
			return nil, errDivisionByZero
		}
		return a / d, nil
	}
	return nil, fmt.Errorf("invalid divisor: '%v'", b)
}

// modulo is the remainder of two integers as an integer, which the engine
// computes as a float, and the float remainder of anything else. The sign
// is the dividend's either way.
func modulo(a, b any) (any, error) {
	if isInteger(a) && isInteger(b) && toInt64(b) != 0 {
		return int(toInt64(a) % toInt64(b)), nil
	}
	x, err := asFloat(a)
	if err != nil {
		return nil, err
	}
	y, err := asFloat(b)
	if err != nil {
		return nil, err
	}
	return math.Mod(x, y), nil
}

// absolute is an integer's magnitude as an integer, which the engine computes
// as a float, and a float's otherwise.
func absolute(a any) (any, error) {
	if isInteger(a) {
		if n := toInt64(a); n != math.MinInt64 {
			if n < 0 {
				n = -n
			}
			return int(n), nil
		}
	}
	x, err := asFloat(a)
	if err != nil {
		return nil, err
	}
	return math.Abs(x), nil
}

// asFloat converts v as the engine converts a filter's float argument.
func asFloat(v any) (float64, error) {
	converted, err := values.Convert(v, float64Type)
	if err != nil {
		return 0, err //nolint:wrapcheck // the engine's own conversion error is what a template author reads
	}
	f, _ := converted.(float64)
	return f, nil
}

// isInteger reports an integer that fits an int64.
func isInteger(v any) bool {
	switch val := v.(type) {
	case int, int8, int16, int32, int64, uint8, uint16, uint32:
		return true
	case uint:
		return uint64(val) <= math.MaxInt64
	case uint64:
		return val <= math.MaxInt64
	}
	return false
}

// toInt64 is v as an int64; the caller has checked isInteger.
func toInt64(v any) int64 {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u := rv.Uint(); u <= math.MaxInt64 {
			return int64(u)
		}
	}
	return 0
}

// float64Bits is the precision a string operand is parsed at.
const float64Bits = 64

// toFloat64 is v as a float64, a string parsed and anything else 0, as the
// engine's arithmetic reads an operand that is not an integer.
func toFloat64(v any) float64 {
	if isInteger(v) {
		return float64(toInt64(v))
	}
	switch val := v.(type) {
	case uint:
		return float64(val)
	case uint64:
		return float64(val)
	case float32:
		return float64(val)
	case float64:
		return val
	case string:
		f, err := strconv.ParseFloat(val, float64Bits)
		if err != nil {
			return 0
		}
		return f
	}
	return 0
}

// decodeJSON decodes a JSON record with every whole number an int, as a YAML
// record's already is: a count read from a JSON record is then a range bound
// like one read from YAML (#2633). A number with a fraction or an exponent
// stays a float.
func decodeJSON(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	decoded := map[string]any{}
	if err := dec.Decode(&decoded); err != nil {
		return nil, err //nolint:wrapcheck // the caller says which record
	}
	if dec.More() {
		return nil, errors.New("invalid character after top-level value")
	}
	out, _ := numbers(decoded).(map[string]any)
	return out, nil
}

// numbers replaces every json.Number in a decoded value.
func numbers(v any) any {
	switch val := v.(type) {
	case json.Number:
		if n, err := val.Int64(); err == nil {
			return int(n)
		}
		f, _ := val.Float64()
		return f
	case map[string]any:
		for k, item := range val {
			val[k] = numbers(item)
		}
		return val
	case []any:
		for i, item := range val {
			val[i] = numbers(item)
		}
		return val
	}
	return v
}
