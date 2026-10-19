package botcheck

import _ "embed"

// SolverJS is the browser script a page answers a bot check with, served at
// /botcheck/solver.js.
//
//go:embed solver.js
var SolverJS []byte
