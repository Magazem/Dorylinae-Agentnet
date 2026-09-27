//go:build race

package relay_test

// raceEnabled: the race detector multiplies memory use, so memory bounds are
// not measured under it.
const raceEnabled = true
