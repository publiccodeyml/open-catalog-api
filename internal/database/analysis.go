package database

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errUnsupportedAnalysisDialect = errors.New("unsupported database dialect for analysis merge")

// MergeAnalysis atomically replaces the namespaces in patch while preserving
// every namespace that is already stored but absent from patch.
func MergeAnalysis(
	gormdb *gorm.DB,
	model any,
	patch common.AnalysisData,
	query string,
	args ...any,
) (common.AnalysisData, error) {
	if patch == nil {
		patch = common.AnalysisData{}
	}

	expression, err := analysisMergeExpression(gormdb.Name(), patch)
	if err != nil {
		return nil, err
	}

	var stored struct {
		Analysis common.AnalysisData `gorm:"type:jsonb"`
	}

	err = gormdb.Transaction(func(transaction *gorm.DB) error {
		result := transaction.Model(model).Where(query, args...).Update("analysis", expression)
		if result.Error != nil {
			return fmt.Errorf("update analysis: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		if err := transaction.Model(model).
			Select("analysis").
			Where(query, args...).
			Take(&stored).Error; err != nil {
			return fmt.Errorf("read merged analysis: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("merge analysis: %w", err)
	}

	if stored.Analysis == nil {
		return common.AnalysisData{}, nil
	}

	return stored.Analysis, nil
}

func analysisMergeExpression(dialect string, patch common.AnalysisData) (clause.Expr, error) {
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return clause.Expr{}, fmt.Errorf("marshal analysis patch: %w", err)
	}

	switch dialect {
	case Postgres:
		return gorm.Expr(
			"COALESCE(analysis, '{}'::jsonb) || CAST(? AS jsonb)",
			string(patchJSON),
		), nil
	case SQLite:
		// json_patch cannot be used here: its recursive merge would treat a
		// null inside a namespace as a deletion. Rebuild only the top-level
		// object so namespace values remain opaque and are replaced whole.
		return gorm.Expr(
			`(
				SELECT json_group_object(merged.key, json(merged.value))
				FROM (
					SELECT key, value
					FROM json_each(COALESCE(analysis, '{}'))
					WHERE key NOT IN (SELECT key FROM json_each(json(?)))
					UNION ALL
					SELECT key, value FROM json_each(json(?))
				) AS merged
			)`,
			string(patchJSON),
			string(patchJSON),
		), nil
	default:
		return clause.Expr{}, fmt.Errorf("%w: %s", errUnsupportedAnalysisDialect, dialect)
	}
}
