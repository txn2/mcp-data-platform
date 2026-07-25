package pkcorpus

import (
	"strings"
	"testing"
)

// TestValidateScenariosRefusals checks every way a scenario set can be
// unrunnable is caught before a run starts, not partway through.
func TestValidateScenariosRefusals(t *testing.T) {
	ok := Scenarios()[0]
	cases := []struct {
		name string
		set  []Scenario
		want string
	}{
		{"duplicate id", []Scenario{ok, ok}, "duplicate scenario id"},
		{"no budget", []Scenario{{ID: "x", Class: ClassPerishable, World: ok.World, Prompt: "p"}}, "no tool-call budget"},
		{"no prompt", []Scenario{{ID: "x", Class: ClassPerishable, World: ok.World, Budget: 1}}, "has no prompt"},
		{"unknown world", []Scenario{{ID: "x", Class: ClassPerishable, World: "nope", Prompt: "p", Budget: 1}}, "not in the fixture registry"},
		{"missing class", []Scenario{ok}, "no scenario reaches the"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateScenarios(c.set)
			if err == nil {
				t.Fatal("accepted an unrunnable scenario set")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
	if err := validateScenarios(Scenarios()); err != nil {
		t.Errorf("committed scenario set does not validate: %v", err)
	}
}
