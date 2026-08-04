//go:build !darwin && !windows

package caffeine

// stubHold is a no-op on platforms without a power-assertion API.
type stubHold struct{}

func newPlatformHold() platformHold { return stubHold{} }

func (stubHold) acquire() error { return nil }
func (stubHold) release()       {}
