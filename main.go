// Command erp-purchase runs the purchase component alone (serve, migrate); the shells compile in purchase.Definition().
package main

import (
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/erp-purchase/v3/purchase"
)

func main() { launcher.Main(purchase.Definition()) }
