//go:build cgo

package common

import (
	"errors"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func duplicateFieldSQLite(err error) *string {
	sqliteErr, ok := errors.AsType[sqlite3.Error](err)
	if !ok {
		return nil
	}

	if sqliteErr.ExtendedCode != sqlite3.ErrConstraintUnique &&
		sqliteErr.ExtendedCode != sqlite3.ErrConstraintPrimaryKey {
		return nil
	}

	msg := sqliteErr.Error()
	if _, tableCols, ok := strings.Cut(msg, "UNIQUE constraint failed: "); ok {
		firstCol, _, _ := strings.Cut(tableCols, ",")
		field := sqliteColToAPI[firstCol]

		return &field
	}

	empty := ""

	return &empty
}
