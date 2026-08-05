package constant

import "sync/atomic"

var setupCompleted atomic.Bool

func SetupCompleted() bool {
	return setupCompleted.Load()
}

func SetSetupCompleted(completed bool) {
	setupCompleted.Store(completed)
}
