// Package fragmented is a cohesion-gate test fixture: two independent islands
// of declarations (order handling, temperature conversion) that never touch.
// It lives under testdata/ so the real cohesion gate (which scans ./...) does
// not evaluate it; TestCohesionDetectsFragmentation loads it explicitly.
package fragmented

// Island A: order handling, cohering through *Order.
type Order struct{ ID string }

func NewOrder(id string) *Order { return &Order{ID: id} }

func (o *Order) Validate() bool { return o.ID != "" }

func priceOrder(o *Order) int {
	if o.Validate() {
		return 10
	}
	return 0
}

// TotalOrder is exported and cohering through the Island-A helpers.
func TotalOrder(o *Order) int { return priceOrder(o) }

// Island B: temperature conversion, cohering through Celsius.
type Celsius float64

func FromFahrenheit(f float64) Celsius { return Celsius((f - 32) * 5 / 9) }

func (c Celsius) ToFahrenheit() float64 { return float64(c)*9/5 + 32 }

func normalize(c Celsius) Celsius { return c }

// Round is exported and cohering through the Island-B helpers.
func Round(c Celsius) Celsius { return normalize(c) }
