//go:build race

package overlay

// The race detector slows Render by an order of magnitude on a loaded
// runner; the budget guards the binary users run.
const budgetFactor = 5
