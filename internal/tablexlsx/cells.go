package tablexlsx

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// cellKind is how one value is stored in the file.
type cellKind uint8

const (
	cellEmpty cellKind = iota
	cellText
	// cellNumber carries its value as decimal text, written into the cell's
	// value verbatim (excelize SetCellDefault): the file holds "1234.50",
	// exactly, not the float64 nearest to it.
	cellNumber
	cellBool
)

// cell is one value ready to write.
type cell struct {
	kind cellKind
	text string
	flag bool
}

// maxSignificantDigits is the precision Excel keeps for a number. A value
// with more significant digits than this is changed by the application that
// opens the file, whatever the file says, so it is refused rather than
// written and silently rounded.
const maxSignificantDigits = 15

// maxExactInteger is the largest integer with maxSignificantDigits digits.
const maxExactInteger = 999_999_999_999_999

// decimalPattern is the exact decimal text a currency, decimal or percent
// cell accepts: an optional minus, digits, and an optional fraction.
var decimalPattern = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// excelEpoch is day zero of Excel's 1900 date system for every date from
// 1900-03-01 on. Excel counts a 1900-02-29 that never was, so earlier dates
// have no serial that means the same day in every reader, and are refused.
var (
	excelEpoch     = time.Date(1899, time.December, 30, 0, 0, 0, 0, time.UTC)
	firstExactDate = time.Date(1900, time.March, 1, 0, 0, 0, 0, time.UTC)
	lastExcelDate  = time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
)

// dateLayout is the one form a date column accepts.
const dateLayout = "2006-01-02"

// dateTimeLayouts are the forms a datetime column accepts. A fraction of a
// second is accepted after the seconds in each (time.Parse reads one there
// even when the layout names none), and a zone offset is read and then
// dropped: a spreadsheet cell has no zone, so the cell shows the clock time
// written.
var dateTimeLayouts = []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", dateLayout}

// Arguments to strconv: base 10, and 64-bit integers and floats.
const (
	decimalBase = 10
	bitSize64   = 64
)

// nanosPerDay converts a time of day to the fraction of a day Excel stores.
const nanosPerDay = float64(24 * time.Hour)

// toCell converts one value for a column of type t ("" for a column that
// declared no type). None is an empty cell in every column.
func toCell(t ColumnType, v any) (cell, error) {
	if v == nil {
		return cell{}, nil
	}
	switch t {
	case TypeString:
		return textCell(v)
	case TypeInteger:
		return integerCell(v)
	case TypeDecimal, TypePercent:
		return decimalCell(v)
	case TypeCurrency:
		return currencyCell(v)
	case TypeDate:
		return dateCell(v)
	case TypeDateTime:
		return dateTimeCell(v)
	default:
		return inferredCell(v)
	}
}

// inferredCell writes a value in an undeclared column as what it is.
func inferredCell(v any) (cell, error) {
	switch n := v.(type) {
	case int64:
		return cell{kind: cellNumber, text: strconv.FormatInt(n, decimalBase)}, nil
	case float64:
		return floatCell(n)
	case bool:
		return cell{kind: cellBool, flag: n}, nil
	default:
		return textCell(v)
	}
}

// textCell writes any value as text: a string as itself, a number or bool
// as its literal, a list or dict as its JSON text.
func textCell(v any) (cell, error) {
	var s string
	switch n := v.(type) {
	case string:
		s = n
	case int64:
		s = strconv.FormatInt(n, decimalBase)
	case float64:
		s = strconv.FormatFloat(n, 'g', -1, bitSize64)
	case bool:
		s = strconv.FormatBool(n)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return cell{}, fmt.Errorf("the value cannot be written as text: %w", err)
		}
		s = string(data)
	}
	if err := checkText(s); err != nil {
		return cell{}, fmt.Errorf("the text %w", err)
	}
	return cell{kind: cellText, text: s}, nil
}

// integerCell writes a whole number: an integer, a float with no fraction,
// or the integer's text.
func integerCell(v any) (cell, error) {
	var n int64
	switch x := v.(type) {
	case int64:
		n = x
	case float64:
		if x != math.Trunc(x) || math.Abs(x) > maxExactInteger {
			return cell{}, fmt.Errorf("an integer column holds whole numbers of at most %d digits, got %v", maxSignificantDigits, x)
		}
		n = int64(x)
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(x), decimalBase, bitSize64)
		if err != nil {
			return cell{}, fmt.Errorf("an integer column holds whole numbers, got %q", x)
		}
		n = parsed
	default:
		return cell{}, fmt.Errorf("an integer column holds whole numbers, got %s", describe(v))
	}
	if n > maxExactInteger || n < -maxExactInteger {
		return cell{}, fmt.Errorf("the integer %d has more than the %d digits Excel keeps; declare the column string to keep every digit", n, maxSignificantDigits)
	}
	return cell{kind: cellNumber, text: strconv.FormatInt(n, decimalBase)}, nil
}

// decimalCell writes a decimal or percent value: its exact decimal text, an
// integer, or a float. A percent is the fraction, so 0.125 shows as 12.50%.
func decimalCell(v any) (cell, error) {
	switch x := v.(type) {
	case string:
		return exactDecimal(x)
	case int64:
		return integerCell(x)
	case float64:
		return floatCell(x)
	default:
		return cell{}, fmt.Errorf("a decimal column holds numbers or their decimal text, got %s", describe(v))
	}
}

// currencyCell writes an amount from its exact decimal text ("1234.50") or
// from integer cents (123450). A float is refused: it is already not the
// amount the script meant, and the platform will not guess which one it was.
func currencyCell(v any) (cell, error) {
	switch x := v.(type) {
	case string:
		return exactDecimal(x)
	case int64:
		return exactDecimal(centsText(x))
	case float64:
		return cell{}, fmt.Errorf(`a currency column takes the exact decimal text, such as "1234.50", or integer cents, such as 123450; the float %v is not exact`, x)
	default:
		return cell{}, fmt.Errorf(`a currency column takes the exact decimal text, such as "1234.50", or integer cents, got %s`, describe(v))
	}
}

// centsText renders integer cents as exact decimal text by placing the point
// in the integer's own digits: 123450 is "1234.50" and -5 is "-0.05". No
// arithmetic is done on the amount, so nothing can overflow or round.
func centsText(cents int64) string {
	const fractionDigits = 2
	digits := strconv.FormatInt(cents, decimalBase)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if len(digits) <= fractionDigits {
		digits = strings.Repeat("0", fractionDigits+1-len(digits)) + digits
	}
	cut := len(digits) - fractionDigits
	return sign + digits[:cut] + "." + digits[cut:]
}

// exactDecimal writes decimal text as the cell's value, unchanged.
func exactDecimal(s string) (cell, error) {
	s = strings.TrimSpace(s)
	if !decimalPattern.MatchString(s) {
		return cell{}, fmt.Errorf(`the text %q is not a decimal number; write digits with an optional minus and fraction, such as "-1234.50"`, s)
	}
	if n := significantDigits(s); n > maxSignificantDigits {
		return cell{}, fmt.Errorf("the number %s has %d significant digits, more than the %d Excel keeps; declare the column string to keep every digit", s, n, maxSignificantDigits)
	}
	return cell{kind: cellNumber, text: s}, nil
}

// significantDigits counts the digits of decimal text that carry precision:
// leading zeros of the whole part and trailing zeros of the fraction do not.
func significantDigits(s string) int {
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	digits := strings.TrimLeft(whole+strings.TrimRight(frac, "0"), "0")
	return len(digits)
}

// floatCell writes a float in its shortest exact text. NaN and the
// infinities have no cell value and are refused.
func floatCell(f float64) (cell, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return cell{}, fmt.Errorf("the value %v is not a number a cell can hold", f)
	}
	return cell{kind: cellNumber, text: strconv.FormatFloat(f, 'g', -1, bitSize64)}, nil
}

// dateCell writes a date from its "YYYY-MM-DD" text as Excel's day serial.
func dateCell(v any) (cell, error) {
	s, ok := v.(string)
	if !ok {
		return cell{}, fmt.Errorf(`a date column takes the date as text, "YYYY-MM-DD", got %s`, describe(v))
	}
	t, err := time.Parse(dateLayout, strings.TrimSpace(s))
	if err != nil {
		return cell{}, fmt.Errorf(`the text %q is not a date; write "YYYY-MM-DD"`, s)
	}
	days, err := serialDays(t)
	if err != nil {
		return cell{}, err
	}
	return cell{kind: cellNumber, text: strconv.Itoa(days)}, nil
}

// dateTimeCell writes a date and time as Excel's serial: the day, plus the
// time of day as a fraction of it.
func dateTimeCell(v any) (cell, error) {
	s, ok := v.(string)
	if !ok {
		return cell{}, fmt.Errorf(`a datetime column takes the time as text, such as "2026-09-01T14:30:00", got %s`, describe(v))
	}
	t, err := parseDateTime(strings.TrimSpace(s))
	if err != nil {
		return cell{}, err
	}
	days, err := serialDays(t)
	if err != nil {
		return cell{}, err
	}
	sinceMidnight := time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute +
		time.Duration(t.Second())*time.Second + time.Duration(t.Nanosecond())
	serial := float64(days) + float64(sinceMidnight)/nanosPerDay
	return cell{kind: cellNumber, text: strconv.FormatFloat(serial, 'f', -1, bitSize64)}, nil
}

// parseDateTime reads a datetime in any of the accepted forms.
func parseDateTime(s string) (time.Time, error) {
	for _, layout := range dateTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf(`the text %q is not a date and time; write "YYYY-MM-DDTHH:MM:SS", with an optional fraction and zone`, s)
}

// errDateRange is the refusal for a date Excel cannot show as itself.
var errDateRange = errors.New("is outside the dates Excel shows exactly, 1900-03-01 to 9999-12-31")

// serialDays is the day serial of t's calendar date, read in t's own zone:
// the date written is the date shown.
func serialDays(t time.Time) (int, error) {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if day.Before(firstExactDate) || day.After(lastExcelDate) {
		return 0, fmt.Errorf("the date %s %w", day.Format(dateLayout), errDateRange)
	}
	// In seconds rather than as a time.Duration, which saturates at 292 years.
	const secondsPerDay = 24 * 60 * 60
	return int((day.Unix() - excelEpoch.Unix()) / secondsPerDay), nil
}
