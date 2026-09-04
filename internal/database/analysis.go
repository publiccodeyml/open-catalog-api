package database

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errUnsupportedAnalysisDialect = errors.New("unsupported database dialect for analysis merge")

// MergeAnalysis atomically replaces the namespaces in patch while preserving
// every namespace that is already stored but absent from patch. It writes
// the row the model's primary key addresses and reads the merged document
// back from the same statement.
func MergeAnalysis(
	gormdb *gorm.DB,
	model models.Analyzable,
	patch common.AnalysisData,
) (common.AnalysisData, error) {
	if patch == nil {
		patch = common.AnalysisData{}
	}

	expression, err := analysisMergeExpression(gormdb.Name(), patch)
	if err != nil {
		return nil, err
	}

	err = models.Transaction(gormdb, func(transaction *gorm.DB) error {
		result := transaction.Model(model).
			Clauses(clause.Returning{Columns: []clause.Column{{Name: "analysis"}}}).
			Update("analysis", expression)
		if result.Error != nil {
			return fmt.Errorf("update analysis: %w", result.Error)
		}

		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("merge analysis: %w", err)
	}

	merged := model.AnalysisDocument()
	if merged == nil {
		return common.AnalysisData{}, nil
	}

	return merged, nil
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
		// The value column of json_each loses the JSON type of an atom: a
		// string comes back unquoted, true and false as 1 and 0. The ->
		// operator returns every value as JSON text, so the namespace is read
		// with it from the document instead.
		return gorm.Expr(
			`(
				WITH patch AS MATERIALIZED (
					SELECT key, json -> fullkey AS value FROM json_each(json(?))
				)
				SELECT json_group_object(merged.key, json(merged.value))
				FROM (
					SELECT key, json -> fullkey AS value
					FROM json_each(COALESCE(analysis, '{}'))
					WHERE key NOT IN (SELECT key FROM patch)
					UNION ALL
					SELECT key, value FROM patch
				) AS merged
			)`,
			string(patchJSON),
		), nil
	default:
		return clause.Expr{}, fmt.Errorf("%w: %s", errUnsupportedAnalysisDialect, dialect)
	}
}
