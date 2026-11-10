package orders

import (
	"os"
	"testing"

	core "github.com/brickKit/erp-purchase/v3/internal/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheLifecycleDecidesEveryCell: every state decides every command, each cell complete for its effect.
func TestTheLifecycleDecidesEveryCell(t *testing.T) {
	assert.Empty(t, core.TableProblems(Lifecycle))
}

// TestTheDesignDocumentIsTheLifecycle: the table in docs/design.md ("订单的状态") is this table, cell by cell.
func TestTheDesignDocumentIsTheLifecycle(t *testing.T) {
	doc, err := os.ReadFile("../../docs/design.md")
	require.NoError(t, err)
	assert.Empty(t, core.DocumentProblems(Lifecycle, string(doc)))
}
