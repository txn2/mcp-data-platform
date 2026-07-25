package pkcorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// Volatility classes a scenario's fact belongs to.
const (
	ClassPerishable = "perishable"
	ClassDurable    = "durable"
	ClassEternal    = "eternal"
)

// Scenario is one capture episode's setup: the world the account is in and
// the analyst question that walks the agent into the fact.
//
// Prompts are analyst questions, never instructions about what to record or
// how to word it. The corpus's whole purpose is to establish that the
// phrasing under study is what this platform's capture actually produces,
// so anything the harness dictates about the wording would destroy the
// evidence it is collecting.
type Scenario struct {
	// ID is unique across the set and names the archived episodes.
	ID string `json:"id"`
	// Class is the volatility class of the fact the episode should reach.
	Class string `json:"class"`
	// World is the fixture world profile the episode runs in.
	World string `json:"world"`
	// Prompt is the analyst question.
	Prompt string `json:"prompt"`
	// Budget caps tool calls for the episode.
	Budget int `json:"budget"`
	// Reaches records, for the archive, what the world makes true. It
	// documents what a faithful capture would be about; it is never shown
	// to the agent.
	Reaches string `json:"reaches"`
}

// corpusBudget is the per-episode tool-call budget. Generous: an episode
// that runs out of budget before capturing contributes nothing to the
// corpus, and the run is free.
const corpusBudget = 30

// Scenarios returns the committed capture-corpus scenario set.
//
// The perishable class carries both directions, because a belief captured
// about an empty account and a belief captured about a populated one go
// stale in opposite ways and the study seeds both. The durable and eternal
// scenarios exist so the frozen seed set has real captured prose for the
// discriminant controls too, rather than prose the study wrote for them.
func Scenarios() []Scenario {
	return []Scenario{
		{
			ID: "perishable-absent", Class: ClassPerishable, World: "monitors-0",
			Prompt: "Report the volume and sentiment trend for ACME's listening monitors over June 2026 (1 June to 28 June). " +
				"Give the daily figures if you can get them, and say what the overall picture is.",
			Budget:  corpusBudget,
			Reaches: "no monitors are provisioned, so the trend has no valid call; owned-profile analytics carry no sentiment",
		},
		{
			ID: "perishable-present", Class: ClassPerishable, World: "monitors-3",
			Prompt: "Report the volume and sentiment trend for ACME's listening monitors over June 2026 (1 June to 28 June). " +
				"Give the daily figures if you can get them, and say what the overall picture is.",
			Budget:  corpusBudget,
			Reaches: "three monitors are provisioned, so the trend is answerable",
		},
		{
			ID: "perishable-forbidden", Class: ClassPerishable, World: "monitors-0-forbidden",
			Prompt: "Report the volume and sentiment trend for ACME's listening monitors over June 2026 (1 June to 28 June). " +
				"Give the daily figures if you can get them, and say what the overall picture is.",
			Budget:  corpusBudget,
			Reaches: "the credential is not entitled to listening, which is not the same as nothing being provisioned",
		},
		{
			ID: "durable-granularity", Class: ClassDurable, World: "monitors-0",
			Prompt: "Report impressions and engagements for ACME's main owned profile over June 2026 (1 June to 28 June), " +
				"bucketed by week rather than by day.",
			Budget:  corpusBudget,
			Reaches: "the granularity parameter is accepted and silently ignored under this contract version",
		},
		{
			ID: "eternal-unique-reach", Class: ClassEternal, World: "monitors-0",
			Prompt: "How many distinct accounts did ACME's main owned profile reach over the whole of June 2026 " +
				"(1 June to 28 June)?",
			Budget:  corpusBudget,
			Reaches: "daily unique counts must not be summed to a period unique; the aggregate reports the deduplicated figure",
		},
	}
}

// System is the fixed scaffold every capture episode runs under. It
// establishes that the agent works across sessions and should record what
// it learns, and stops there: it says nothing about how to word a record,
// whether to date it, whether to advise re-checking, or what to advise
// against. Steering any of those would author the artifact the study is
// trying to observe.
const System = `You are a data analyst agent connected to a data platform over MCP. You work across many separate sessions and can save knowledge for later and recall it.

Rules:
- Ground every answer in tool results; do not answer from prior knowledge about any specific account or API.
- Use the search tool to discover available data and saved knowledge before querying.
- Do the work the request asks for, and report what you found.
- Before you finish, save what you learned about this account or API using the memory tools, so a future session does not have to rediscover it. Then state in one line what you saved.`

// ValidateScenarios checks the set before a run spends anything on it: ids
// unique, budgets set, every class represented, and every world present in
// the fixture's committed registry. A scenario naming a world the fixture
// does not have would otherwise fail partway through a run, after earlier
// episodes had already been recorded against a world that was never set.
func ValidateScenarios() error {
	return validateScenarios(Scenarios())
}

// validateScenarios is the checkable core: it takes the set so its refusal
// branches are exercisable without editing the committed one.
func validateScenarios(scenarios []Scenario) error {
	seen := map[string]bool{}
	classes := map[string]int{}
	for _, sc := range scenarios {
		switch {
		case seen[sc.ID]:
			return fmt.Errorf("pkcorpus: duplicate scenario id %s", sc.ID)
		case sc.Budget <= 0:
			return fmt.Errorf("pkcorpus: scenario %s has no tool-call budget", sc.ID)
		case sc.Prompt == "":
			return fmt.Errorf("pkcorpus: scenario %s has no prompt", sc.ID)
		}
		if _, ok := apigen.WorldByName(sc.World); !ok {
			return fmt.Errorf("pkcorpus: scenario %s names world %q, which is not in the fixture registry", sc.ID, sc.World)
		}
		seen[sc.ID] = true
		classes[sc.Class]++
	}
	for _, class := range []string{ClassPerishable, ClassDurable, ClassEternal} {
		if classes[class] == 0 {
			return fmt.Errorf("pkcorpus: no scenario reaches the %s class", class)
		}
	}
	return nil
}

// ScenariosHash is the canonical SHA-256 of the scenario set plus the
// system scaffold, recorded in every corpus manifest so an archived corpus
// names the exact stimulus that produced it.
func ScenariosHash() string {
	payload := struct {
		Scenarios []Scenario `json:"scenarios"`
		System    string     `json:"system"`
	}{Scenarios(), System}
	raw, err := json.Marshal(payload)
	if err != nil {
		// Plain data; marshal cannot fail.
		panic(fmt.Sprintf("pkcorpus: marshal scenarios: %v", err))
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
