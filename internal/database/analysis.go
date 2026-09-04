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

var errUnsupportedDialect = errors.New("unsupported database dialect for analysis merge")

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

	dialect, err := DialectOf(gormdb)
	if err != nil {
		return nil, err
	}

	expression, err := analysisMergeExpression(dialect, patch)
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

// analysisMergeExpression builds the SET expression of the analysis merge.
// Each incoming namespace is compared with the stored one, "t" excluded:
// equal keeps the stored namespace and its timestamp, different writes the
// incoming one. The comparison runs on the row being updated, so a
// namespace another writer changed after the client read it is still
// written.
func analysisMergeExpression(dialect Dialect, patch common.AnalysisData) (clause.Expr, error) {
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return clause.Expr{}, fmt.Errorf("marshal analysis patch: %w", err)
	}

	switch dialect {
	case Postgres:
		// Postgres refuses to delete a key from a jsonb scalar, so when
		// either side is not an object the incoming value is taken as is.
		return gorm.Expr(
			`COALESCE(analysis, '{}'::jsonb) || (
				SELECT COALESCE(jsonb_object_agg(patch.key, CASE
					WHEN jsonb_typeof(analysis -> patch.key) <> 'object'
						OR jsonb_typeof(patch.value) <> 'object' THEN patch.value
					WHEN (analysis -> patch.key) - 't' = patch.value - 't' THEN analysis -> patch.key
					ELSE patch.value
				END), '{}'::jsonb)
				FROM jsonb_each(CAST(? AS jsonb)) AS patch
			)`,
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
		// SQLite compares the two namespaces as text, so the keys must be in
		// the same order on both sides. A namespace written through this API
		// keeps the key order of the request, so it matches. One stored in
		// another order counts as changed and only gets a fresh timestamp.
		return gorm.Expr(
			`(
				WITH patch AS MATERIALIZED (
					SELECT key, json -> fullkey AS value FROM json_each(json(?))
				), stored AS MATERIALIZED (
					SELECT key, json -> fullkey AS value FROM json_each(COALESCE(analysis, '{}'))
				)
				SELECT json_group_object(merged.key, json(merged.value))
				FROM (
					SELECT key, value FROM stored WHERE key NOT IN (SELECT key FROM patch)
					UNION ALL
					SELECT patch.key, CASE
						WHEN json(json_remove(stored.value, '$.t')) = json(json_remove(patch.value, '$.t'))
							THEN stored.value
						ELSE patch.value
					END
					FROM patch LEFT JOIN stored ON stored.key = patch.key
				) AS merged
			)`,
			string(patchJSON),
		), nil
	default:
		return clause.Expr{}, fmt.Errorf("%w: %q", errUnsupportedDialect, dialect)
	}
}
