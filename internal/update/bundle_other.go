//go:build !darwin

package update

func runningAppBundle() string { return "" }

func pickAppZipAsset(assets []ghAsset) ghAsset { return ghAsset{} }

func installAppBundle(zipPath, app string) error { return nil }

func cleanupOldAppBundle() {}

func rollbackAppBundle(app string) error { return nil }
