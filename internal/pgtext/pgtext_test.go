package pgtext

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRange(t *testing.T) {
	cases := []struct {
		in   string
		want Range
	}{
		{"[1,5)", Range{Lower: new("1"), Upper: new("5"), LowerInc: true}},
		{"(,10]", Range{Upper: new("10"), UpperInc: true}},
		{"[3,)", Range{Lower: new("3"), LowerInc: true}},
		{"empty", Range{Empty: true}},
		{
			`["2026-01-01 10:00:00+00","2026-01-01 11:00:00+00")`,
			Range{Lower: new("2026-01-01 10:00:00+00"), Upper: new("2026-01-01 11:00:00+00"), LowerInc: true},
		},
		{`["a\"b","c""d")`, Range{Lower: new(`a"b`), Upper: new(`c"d`), LowerInc: true}},
		{`["",x)`, Range{Lower: new(""), Upper: new("x"), LowerInc: true}},
	}
	for _, c := range cases {
		got, err := ParseRange(c.in)
		require.NoError(t, err, c.in)
		assert.Equal(t, c.want, got, c.in)
		// What FormatRange writes reads back the same.
		again, err := ParseRange(FormatRange(got))
		require.NoError(t, err)
		assert.Equal(t, got, again, c.in)
	}
	for _, bad := range []string{"", "1,5", "[1;5)", "[1,5", `["1,5)`, `[1,5)x`, `[1\`} {
		_, err := ParseRange(bad)
		assert.Error(t, err, bad)
	}
}

func TestParseMultirange(t *testing.T) {
	got, err := ParseMultirange("{[1,3),[5,7)}")
	require.NoError(t, err)
	assert.Equal(t, []Range{{Lower: new("1"), Upper: new("3"), LowerInc: true}, {Lower: new("5"), Upper: new("7"), LowerInc: true}}, got)
	again, err := ParseMultirange(FormatMultirange(got))
	require.NoError(t, err)
	assert.Equal(t, got, again)
	empty, err := ParseMultirange("{}")
	require.NoError(t, err)
	assert.Empty(t, empty)
	for _, bad := range []string{"[1,3)", "{[1,3) [5,7)}", "{x}"} {
		_, err := ParseMultirange(bad)
		assert.Error(t, err, bad)
	}
}

func TestParseComposite(t *testing.T) {
	got, err := ParseComposite(`(1,"two, three",,"",)`)
	require.NoError(t, err)
	assert.Equal(t, []*string{new("1"), new("two, three"), nil, new(""), nil}, got)
	again, err := ParseComposite(FormatComposite(got))
	require.NoError(t, err)
	assert.Equal(t, got, again)
	nested, err := ParseComposite(`(1,"(2,""x"")")`)
	require.NoError(t, err)
	assert.Equal(t, `(2,"x")`, *nested[1])
	for _, bad := range []string{"1,2", `(1,"2)`} {
		_, err := ParseComposite(bad)
		assert.Error(t, err, bad)
	}
}

func TestParseHstore(t *testing.T) {
	got, err := ParseHstore(`"a"=>"1", "b c"=>NULL, "q"=>"say \"hi\""`)
	require.NoError(t, err)
	assert.Equal(t, map[string]*string{"a": new("1"), "b c": nil, "q": new(`say "hi"`)}, got)
	again, err := ParseHstore(FormatHstore([]string{"a", "b c", "q"}, got))
	require.NoError(t, err)
	assert.Equal(t, got, again)
	quotedNull, err := ParseHstore(`"k"=>"NULL"`)
	require.NoError(t, err)
	assert.Equal(t, "NULL", *quotedNull["k"], "a quoted NULL is the string")
	empty, err := ParseHstore("")
	require.NoError(t, err)
	assert.Empty(t, empty)
	_, err = ParseHstore(`"a"="1"`)
	assert.Error(t, err)
}

func TestParseVector(t *testing.T) {
	got, err := ParseVector("[1,2.5,-3e-2]")
	require.NoError(t, err)
	assert.Equal(t, []float64{1, 2.5, -0.03}, got)
	empty, err := ParseVector("[]")
	require.NoError(t, err)
	assert.Empty(t, empty)
	for _, bad := range []string{"1,2", "[1,x]"} {
		_, err := ParseVector(bad)
		assert.Error(t, err, bad)
	}
}
