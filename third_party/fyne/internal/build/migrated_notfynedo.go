//go:build !migrated_fynedo

package build

// DisableThreadChecks disables the thread safety checks for performance.
//
// 1loop: on, as with the migrated_fynedo tag, for every build. 1loop does
// all its interface work through fyne.Do, and the check read the
// goroutine's stack at every refresh (a sixth of a scroll's CPU).
const DisableThreadChecks = true
