//go:build !windows

package mouse

// pressRidesFirstMove is false off Windows: the Linux motion path stays
// byte-identical to what shipped. See press_windows.go for the Windows defect
// this flag works around and why the two hosts differ.
const pressRidesFirstMove = false
