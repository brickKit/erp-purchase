package core

import (
	"github.com/brickKit/be-sdk-go/v3/flow"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Base is what every module works with: the component's runtime (its database, calls, events, audit, logger) and the
// flow kernel the requisitions' and orders' approvals run on (the entry builds it; its hooks call the modules).
type Base struct {
	RT   *runtime.Runtime
	Flow *flow.Kernel
}
