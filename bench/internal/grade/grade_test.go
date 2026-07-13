package grade

import "testing"

func TestExtractFinal(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"marker", "reasoning...\nFINAL ANSWER: 42.50", "42.50"},
		{"last marker wins", "FINAL ANSWER: draft\nmore\nFINAL ANSWER: real", "real"},
		{"case insensitive", "final answer: memory.bench.orders", "memory.bench.orders"},
		{"no marker", "  just text  ", "just text"},
		{"multiline tail", "FINAL ANSWER: 12.30\nbecause of X", "12.30\nbecause of X"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractFinal(c.in); got != c.want {
				t.Errorf("ExtractFinal(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestNumeric(t *testing.T) {
	cases := []struct {
		name     string
		final    string
		expected float64
		tol      float64
		wantGot  float64
		wantOK   bool
		wantHit  bool
	}{
		{"exact", "42.50", 42.5, 0.01, 42.5, true, true},
		{"dollar and commas", "$1,234,567.89", 1234567.89, 0.01, 1234567.89, true, true},
		{"within tolerance", "100.004", 100.0, 0.01, 100.004, true, true},
		{"outside tolerance", "100.02", 100.0, 0.01, 100.02, true, false},
		{"hundredfold cents miss", "4250", 42.5, 0.01, 4250, true, false},
		{"negative", "-12.5", -12.5, 0.01, -12.5, true, true},
		{"first decimal wins", "12345.67 (from 890 orders)", 12345.67, 0.01, 12345.67, true, true},
		{"restated year skipped", "The Q1 2025 total is 1,077,853.21 USD", 1077853.21, 0.01, 1077853.21, true, true},
		{"bare integer answer", "4250 USD", 4250, 0.01, 4250, true, true},
		{"second line ignored", "42.50\nbut 99.99 was gross", 42.5, 0.01, 42.5, true, true},
		{"no number", "unknown", 1, 0.01, 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok, hit := Numeric(c.final, c.expected, c.tol)
			if ok != c.wantOK || hit != c.wantHit || (ok && got != c.wantGot) {
				t.Errorf("Numeric(%q, %v, %v) = (%v, %v, %v), want (%v, %v, %v)",
					c.final, c.expected, c.tol, got, ok, hit, c.wantGot, c.wantOK, c.wantHit)
			}
		})
	}
}

func TestEntity(t *testing.T) {
	aliases := []string{"memory.bench.orders", "bench.orders"}
	wrong := []string{"legacy_orders"}
	cases := []struct {
		name    string
		final   string
		want    string
		correct bool
	}{
		{"qualified", "memory.bench.orders", "memory.bench.orders", true},
		{"case", "MEMORY.BENCH.ORDERS", "memory.bench.orders", true},
		{"embedded", "the table memory.bench.orders is best", "memory.bench.orders", true},
		{"legacy does not match", "memory.bench.legacy_orders", "", false},
		{"bare name does not match", "orders", "", false},
		{"wrong alias vetoes", "memory.bench.legacy_orders (not memory.bench.orders)", "", false},
		{"second line ignored", "memory.bench.orders\nlegacy_orders is deprecated", "memory.bench.orders", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, correct := Entity(c.final, aliases, wrong)
			if got != c.want || correct != c.correct {
				t.Errorf("Entity(%q) = (%q, %v), want (%q, %v)", c.final, got, correct, c.want, c.correct)
			}
		})
	}
}

func TestEntityRegionTrap(t *testing.T) {
	// The top-region trap: naming a losing region anywhere on the answer line
	// is incorrect even when the winner is also mentioned.
	got, correct := Entity("East - though West leads after discounts", []string{"West"}, []string{"North", "South", "East"})
	if correct || got != "" {
		t.Errorf("trap answer graded correct (matched %q)", got)
	}
	if _, ok := Entity("West", []string{"West"}, []string{"North", "South", "East"}); !ok {
		t.Error("clean winner answer graded incorrect")
	}
}
