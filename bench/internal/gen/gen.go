// Package gen is the benchmark's deterministic dataset generator (issue #930
// principle 3: seeded ground truth). One fixed-seed dataset model emits every
// seed artifact — Trino DDL/DML, DataHub metadata proposals, knowledge-page
// SQL — plus the task set whose ground truths are computed directly from the
// generated rows, so truth is derived, never hand-typed. Regeneration is
// byte-identical; a test diffs the committed artifacts against a fresh run.
package gen

import (
	"fmt"
	// nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- deterministic fixture generation from a fixed seed is the point; crypto/rand would break reproducibility
	"math/rand"
	"time"
)

// Seed is the fixed RNG seed. Changing it changes every artifact and ground
// truth together; the committed task-set hash pins the current value.
const Seed = 930

// Dataset dimensions. Small enough to seed in seconds, large enough that no
// answer is guessable without querying.
const (
	customerCount = 80
	orderCount    = 1200
	legacyCount   = 60
)

// regions and tiers are the categorical dimensions.
var (
	regions = []string{"North", "South", "East", "West"}
	tiers   = []string{"basic", "plus", "enterprise"}

	firstNames = []string{"Avery", "Blake", "Casey", "Devon", "Emery", "Finley", "Gray", "Harper", "Indigo", "Jules",
		"Kai", "Lane", "Morgan", "Noel", "Oakley", "Parker", "Quinn", "Reese", "Sage", "Tatum"}
	lastNames = []string{"Alvarez", "Brooks", "Chen", "Dawson", "Ellis", "Foster", "Garcia", "Hayes", "Iwata", "Jensen",
		"Kim", "Lopez", "Mercer", "Nguyen", "Okafor", "Price", "Ramos", "Silva", "Turner", "Vargas"}
)

// Customer is one row of memory.bench.customers.
type Customer struct {
	ID        int
	Name      string
	Region    string
	Tier      string
	CreatedAt time.Time
}

// Order is one row of memory.bench.orders. Amount and Discount are integer US
// cents — the units trap: only the metadata layer records the unit.
type Order struct {
	ID         int
	CustomerID int
	TS         time.Time
	Status     string // completed | refunded | pending
	Amount     int64  // cents
	Discount   int64  // cents
}

// Dataset is the generated world every artifact derives from.
type Dataset struct {
	Customers []Customer
	Orders    []Order
}

// Generate builds the dataset from the fixed seed and asserts the trap
// invariants hold. It panics on invariant violation: the seed is a constant,
// so a violation is a code bug caught by the generator's own tests, never a
// runtime roll of the dice.
func Generate() *Dataset {
	rng := rand.New(rand.NewSource(Seed)) // #nosec G404 -- deterministic fixture generation, not crypto
	ds := &Dataset{
		Customers: genCustomers(rng),
	}
	ds.Orders = genOrders(rng, ds.Customers)
	ds.assertTrapInvariants()
	return ds
}

// genCustomers builds the customer dimension.
func genCustomers(rng *rand.Rand) []Customer {
	base := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	customers := make([]Customer, customerCount)
	for i := range customers {
		customers[i] = Customer{
			ID:        i + 1,
			Name:      fmt.Sprintf("%s %s", firstNames[rng.Intn(len(firstNames))], lastNames[rng.Intn(len(lastNames))]),
			Region:    regions[rng.Intn(len(regions))],
			Tier:      pickTier(rng),
			CreatedAt: base.Add(time.Duration(rng.Intn(700*24)) * time.Hour),
		}
	}
	return customers
}

// pickTier weights tiers toward basic.
func pickTier(rng *rand.Rand) string {
	switch r := rng.Float64(); {
	case r < 0.5:
		return tiers[0]
	case r < 0.8:
		return tiers[1]
	default:
		return tiers[2]
	}
}

// genOrders builds the order fact. Regional biases engineer the net-revenue
// trap: East runs high gross with heavy refunds and discounts, West runs
// moderate gross but clean, so the gross-revenue leader and the policy
// net-revenue leader differ. assertTrapInvariants verifies the result.
func genOrders(rng *rand.Rand, customers []Customer) []Order {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	orders := make([]Order, orderCount)
	for i := range orders {
		c := customers[rng.Intn(len(customers))]
		amount := int64(500 + rng.Intn(249500)) // 5.00 .. 2500.00 USD in cents
		bias := regionBias(c.Region)
		amount = int64(float64(amount) * bias.amountFactor)
		status := pickStatus(rng, bias.refundP)
		var discount int64
		if status == "completed" {
			discount = int64(float64(amount) * bias.discountFrac * rng.Float64())
		}
		orders[i] = Order{
			ID:         1000 + i,
			CustomerID: c.ID,
			TS:         start.Add(time.Duration(rng.Intn(365*24*60)) * time.Minute),
			Status:     status,
			Amount:     amount,
			Discount:   discount,
		}
	}
	return orders
}

// regionTrapBias shapes one region's order economics.
type regionTrapBias struct {
	amountFactor float64
	refundP      float64
	discountFrac float64
}

// regionBias returns the trap-engineering bias for a region.
func regionBias(region string) regionTrapBias {
	switch region {
	case "East":
		return regionTrapBias{amountFactor: 1.6, refundP: 0.35, discountFrac: 0.5}
	case "West":
		return regionTrapBias{amountFactor: 1.25, refundP: 0.05, discountFrac: 0.08}
	default:
		return regionTrapBias{amountFactor: 1.0, refundP: 0.10, discountFrac: 0.15}
	}
}

// pickStatus draws an order status with the region's refund probability.
func pickStatus(rng *rand.Rand, refundP float64) string {
	switch r := rng.Float64(); {
	case r < refundP:
		return "refunded"
	case r < refundP+0.08:
		return "pending"
	default:
		return "completed"
	}
}

// assertTrapInvariants verifies the properties the S3 tasks depend on. A
// violation is a code bug (the seed is constant), caught by the generator's own
// tests, never a runtime roll of the dice.
func (d *Dataset) assertTrapInvariants() {
	grossLeader := d.topRegionGrossAll()
	netLeader := d.TopRegionNet2025()
	if grossLeader == netLeader {
		panic(fmt.Sprintf("net_revenue trap violated: gross leader %s equals net leader %s", grossLeader, netLeader))
	}
	if d.enterpriseOrderCount() == 0 {
		panic("units trap violated: no enterprise orders")
	}
	// fiscal_calendar: the fiscal-2025 window excludes January 2025 (and the
	// orders span adds nothing in January 2026), so the fiscal and calendar
	// net-revenue answers differ iff January 2025 carried completed revenue.
	if d.january2025NetCents() <= 0 {
		panic("fiscal_calendar trap violated: January 2025 net revenue is not positive, so fiscal and calendar answers coincide")
	}
	// freshness_cutoff: December 2025 (after the index cutoff) must carry
	// completed revenue, or the index-vs-orders answers would coincide at zero.
	if d.decemberGrossCents() <= 0 {
		panic("freshness_cutoff trap violated: no completed December 2025 revenue")
	}
	// tier_boundary: the key-account set (plus + enterprise) must exceed the
	// single-top-tier reading (enterprise only), or the trap has no gap.
	if d.KeyAccountCount() <= d.enterpriseCustomerCount() {
		panic("tier_boundary trap violated: no plus-tier customers to separate key-account count from enterprise count")
	}
}
