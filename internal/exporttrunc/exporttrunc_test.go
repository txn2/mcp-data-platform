package exporttrunc

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

var deploymentRows = Limit{
	Applied: 100, Source: SourceDeployment, Unit: UnitRows, Key: "portal.export.max_rows",
	Remedy: "Set limit to export a chosen subset.",
}

var requestRows = Limit{Applied: 10, Source: SourceRequest, Unit: UnitRows, Key: "limit"}

func TestJudge(t *testing.T) {
	for _, tc := range []struct {
		name        string
		j           Judgment
		wantRefusal string // substring; "" means the export writes
		wantNote    string // substring of the note; "" means no note
		wantReport  Report
	}{
		{
			name:       "complete under the deployment cap",
			j:          Judgment{Limit: deploymentRows, Written: 99, WrittenUnit: UnitRows},
			wantReport: Report{LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitRows},
		},
		{
			name: "deployment cut refuses by default",
			j:    Judgment{Limit: deploymentRows, Truncated: true, Written: 100, WrittenUnit: UnitRows},
			wantRefusal: "the result was cut at the deployment cap of 100 rows (portal.export.max_rows): " +
				"the query returned more rows, so nothing was written. Set limit to export a chosen subset. " +
				"To write the first 100 rows anyway, marked incomplete, set on_truncation to \"warn\".",
			wantReport: Report{Truncated: true, LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitRows},
		},
		{
			name: "deployment cut under warn writes and flags",
			j: Judgment{
				Limit: deploymentRows, Truncated: true, Written: 100, WrittenUnit: UnitRows,
				Options: Options{OnTruncation: PolicyWarn},
			},
			wantNote:   "Truncated at the deployment cap of 100 rows (portal.export.max_rows); the query returned more rows. This file is incomplete.",
			wantReport: Report{Truncated: true, LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitRows},
		},
		{
			name:       "request cut writes and flags by default",
			j:          Judgment{Limit: requestRows, Truncated: true, Written: 10, WrittenUnit: UnitRows},
			wantNote:   "Truncated at the requested limit of 10 rows (limit)",
			wantReport: Report{Truncated: true, LimitApplied: 10, LimitSource: SourceRequest, LimitUnit: UnitRows},
		},
		{
			name: "request cut under fail refuses",
			j: Judgment{
				Limit: requestRows, Truncated: true, Written: 10, WrittenUnit: UnitRows,
				Options: Options{OnTruncation: PolicyFail},
			},
			wantRefusal: "cut at the requested limit of 10 rows (limit)",
			wantReport:  Report{Truncated: true, LimitApplied: 10, LimitSource: SourceRequest, LimitUnit: UnitRows},
		},
		{
			name:       "an unordered cut says the subset is arbitrary",
			j:          Judgment{Limit: requestRows, Truncated: true, Written: 10, WrittenUnit: UnitRows, Unordered: true},
			wantNote:   "no top-level ORDER BY",
			wantReport: Report{Truncated: true, LimitApplied: 10, LimitSource: SourceRequest, LimitUnit: UnitRows, ArbitrarySubset: true},
		},
		{
			name:       "an unordered result the limit did not cut is every row",
			j:          Judgment{Limit: requestRows, Written: 4, WrittenUnit: UnitRows, Unordered: true},
			wantReport: Report{LimitApplied: 10, LimitSource: SourceRequest, LimitUnit: UnitRows},
		},
		{
			name: "a page walk at its default bound refuses",
			j: Judgment{
				Limit:     Limit{Applied: 100, Source: SourceDeployment, Unit: UnitPages, Key: "paginate.max_pages default"},
				Truncated: true, Written: 5000, WrittenUnit: "items",
			},
			wantRefusal: "the upstream reported another page, so nothing was written.",
			wantReport:  Report{Truncated: true, LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitPages},
		},
		{
			name: "an exact count missed refuses",
			j: Judgment{
				Limit: deploymentRows, Written: 40, WrittenUnit: UnitRows, Options: Options{ExpectRows: new(41)},
			},
			wantRefusal: "expected exactly 41 rows, got 40; nothing was written.",
			wantReport:  Report{LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitRows},
		},
		{
			name: "a minimum missed under warn writes and flags",
			j: Judgment{
				Limit: deploymentRows, Written: 3, WrittenUnit: UnitRows,
				Options: Options{ExpectMinRows: new(50), OnTruncation: PolicyWarn},
			},
			wantNote: "Count check failed: expected at least 50 rows, got 3.",
			wantReport: Report{
				LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitRows,
				ExpectMismatch: "expected at least 50 rows, got 3",
			},
		},
		{
			name: "a minimum met says nothing",
			j: Judgment{
				Limit: deploymentRows, Written: 50, WrittenUnit: UnitRows, Options: Options{ExpectMinRows: new(50)},
			},
			wantReport: Report{LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitRows},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(tc.j)
			if tc.wantRefusal == "" && got.Refusal != "" {
				t.Fatalf("Refusal = %q; want none", got.Refusal)
			}
			if !strings.Contains(got.Refusal, tc.wantRefusal) {
				t.Fatalf("Refusal = %q; want it to contain %q", got.Refusal, tc.wantRefusal)
			}
			if tc.wantNote == "" && got.Note != "" {
				t.Fatalf("Note = %q; want none", got.Note)
			}
			if !strings.Contains(got.Note, tc.wantNote) {
				t.Fatalf("Note = %q; want it to contain %q", got.Note, tc.wantNote)
			}
			if got.Report != tc.wantReport {
				t.Fatalf("Report = %+v; want %+v", got.Report, tc.wantReport)
			}
		})
	}
}

func TestOptionsValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    Options
		want string
	}{
		{"empty", Options{}, ""},
		{"fail", Options{OnTruncation: PolicyFail}, ""},
		{"warn with a minimum", Options{OnTruncation: PolicyWarn, ExpectMinRows: new(0)}, ""},
		{"unknown policy", Options{OnTruncation: "ignore"}, `on_truncation must be "fail" or "warn", not "ignore"`},
		{"both expectations", Options{ExpectRows: new(1), ExpectMinRows: new(1)}, "not both"},
		{"negative", Options{ExpectRows: new(-1)}, "may not be negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.o.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("Validate() = %v; want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v; want it to contain %q", err, tc.want)
			}
		})
	}
	if (Options{}).Expects() || !(Options{ExpectMinRows: new(0)}).Expects() {
		t.Fatal("Expects() reads the expectation fields wrong")
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	if (Report{LimitApplied: 5}).Metadata() != nil {
		t.Fatal("a result the bound did not cut records no metadata")
	}
	r := Report{Truncated: true, LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitRows, ArbitrarySubset: true}
	m := r.Metadata()
	keys := []string{MetaTruncated, MetaLimitApplied, MetaLimitSource, MetaLimitUnit}
	if len(m) != len(keys) {
		t.Fatalf("Metadata() = %v; want exactly the truncation keys", m)
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Fatalf("Metadata() lacks %q", k)
		}
	}
	// What a JSONB column hands back: numbers as float64.
	stored := map[string]any{MetaTruncated: true, MetaLimitApplied: float64(100), MetaLimitSource: SourceDeployment, MetaLimitUnit: UnitRows, "run_id": "r1"}
	got := FromMetadata(stored)
	want := &Report{Truncated: true, LimitApplied: 100, LimitSource: SourceDeployment, LimitUnit: UnitRows}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FromMetadata() = %+v; want %+v", got, want)
	}
	if FromMetadata(map[string]any{"run_id": "r1"}) != nil || FromMetadata(nil) != nil {
		t.Fatal("metadata recording no cut reads back as no report")
	}
	carried := Carry(stored)
	if truncated, _ := carried[MetaTruncated].(bool); !truncated || carried[MetaLimitApplied] != 100 || carried["run_id"] != nil {
		t.Fatalf("Carry() = %v; want only the truncation keys", carried)
	}
	if Carry(map[string]any{"run_id": "r1"}) != nil {
		t.Fatal("Carry() of a complete version carries nothing")
	}
}

func TestIntValue(t *testing.T) {
	for _, v := range []any{float64(7), 7, int64(7)} {
		if intValue(v) != 7 {
			t.Fatalf("intValue(%T) != 7", v)
		}
	}
	if intValue("7") != 0 {
		t.Fatal("intValue of a string is 0")
	}
}

func TestWithTag(t *testing.T) {
	if got := WithTag([]string{"a"}, Report{}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("WithTag(complete) = %v", got)
	}
	cut := Report{Truncated: true}
	if got := WithTag([]string{"a"}, cut); !reflect.DeepEqual(got, []string{"a", Tag}) {
		t.Fatalf("WithTag(cut) = %v", got)
	}
	if got := WithTag([]string{Tag}, cut); !reflect.DeepEqual(got, []string{Tag}) {
		t.Fatalf("WithTag(already tagged) = %v", got)
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 100000: "100,000", 1234567: "1,234,567", -4500: "-4,500"} {
		if got := Thousands(n); got != want {
			t.Errorf("Thousands(%d) = %q; want %q", n, got, want)
		}
	}
}

func TestWalkLimit(t *testing.T) {
	set := WalkLimit(25, true, "ignored")
	if set.Source != SourceRequest || set.Applied != 25 || set.Unit != UnitPages || set.Key != "paginate.max_pages" || set.Remedy != "" {
		t.Fatalf("WalkLimit(caller's) = %+v", set)
	}
	def := WalkLimit(100, false, "Set paginate.max_pages.")
	if def.Source != SourceDeployment || def.Remedy != "Set paginate.max_pages." || def.name() != "the default page bound" {
		t.Fatalf("WalkLimit(default) = %+v", def)
	}
	var props map[string]any
	if err := json.Unmarshal([]byte("{"+WalkSchemaProperties+"}"), &props); err != nil {
		t.Fatalf("WalkSchemaProperties is not a JSON object body: %v", err)
	}
	for _, name := range []string{"on_truncation", "expect_rows", "expect_min_rows"} {
		if _, ok := props[name]; !ok {
			t.Errorf("WalkSchemaProperties lacks %q", name)
		}
	}
}

func TestValidateWalk(t *testing.T) {
	if err := (Options{ExpectRows: new(1)}).ValidateWalk(true); err != nil {
		t.Fatalf("an expectation on a walk is valid: %v", err)
	}
	if err := (Options{ExpectRows: new(1)}).ValidateWalk(false); !errors.Is(err, ErrExpectNeedsWalk) {
		t.Fatalf("an expectation on one response = %v; want ErrExpectNeedsWalk", err)
	}
	if err := (Options{OnTruncation: "x"}).ValidateWalk(true); err == nil {
		t.Fatal("an invalid policy is refused on a walk too")
	}
	if got := Sentences("a.", "", "b."); got != "a. b." {
		t.Fatalf("Sentences = %q", got)
	}
}
