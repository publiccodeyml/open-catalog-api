package general

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestClausesQuotesSearchColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)

	app, ctx := newTestCtx()
	defer app.ReleaseCtx(ctx)

	ctx.Request().URI().SetQueryString("search=abc")

	stmt := db.Session(&gorm.Session{DryRun: true}).Table("logs")

	ret, err := Clauses(ctx, stmt, "", "name")
	require.NoError(t, err)

	sql := ret.Find(&[]struct{}{}).Statement.SQL.String()
	assert.Contains(t, sql, `LOWER(`+"`name`"+`)`)
}
