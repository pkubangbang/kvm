package mouse

import (
	"fmt"
	"os"
)

// tracef is diagnostic breadcrumb output, enabled only when KVM_DEBUG_MOTION is
// set. It exists so a motion leg can be replayed from the log instead of being
// inferred from its outcome: "landed 14 px off" says nothing about which sample
// the controller believed.
func tracef(format string, args ...any) {
	if os.Getenv("KVM_DEBUG_MOTION") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "motion: "+format+"\n", args...)
}
