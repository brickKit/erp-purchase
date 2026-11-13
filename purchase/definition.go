// Package purchase is the purchase component: requisitions, purchase orders and their approval, receipts through the
// inventory, service acceptances and purchase returns; the valuation bases erp/costing takes and the match basis
// erp/finance reads (protocols 05 and 05b).
package purchase

import (
	"embed"
	"io/fs"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/brickKit/be-sdk-go/v3/component"
	"github.com/brickKit/be-sdk-go/v3/events"
	"github.com/brickKit/be-sdk-go/v3/flow"
	"github.com/brickKit/be-sdk-go/v3/runtime"
)

// Identity.
const (
	Version  = "3.0.27"
	GRPCPort = 9095
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func migrations() fs.FS {
	sub, _ := fs.Sub(migrationFiles, "migrations")
	return sub
}

// Own describes the own keys for the manifest's configSchema.
var Own = map[string]string{
	flow.ActionSourcesKey: "the channel members whose button actions the approvals take: component IDs, comma separated " +
		"(e.g. integration/im-dingtalk); unset: none (no buttons, the link still works)",
}

// Definition is the component as the launcher and the shells take it.
func Definition() component.Definition {
	return component.Definition{ID: core.ID, Version: Version, Keys: flow.Keys(), Migrations: migrations(), GRPCPort: GRPCPort,
		Subscribes: true,
		Streams:    []events.Stream{{Name: core.Stream, Partitions: 4}}, Authz: core.Definitions(), Resend: core.OpResend,
		Recovery: core.OpRecover, New: func(rt *runtime.Runtime) (component.Module, error) { return New(rt) }}
}
