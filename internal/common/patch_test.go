package common_test

import (
	"strings"
	"testing"

	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
)

func TestApplyPatchJSONPatchValidation(t *testing.T) {
	t.Run("empty patch returns error", func(t *testing.T) {
		entity := sampleEntity()

		_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, []byte(`[]`))
		assert.Equal(t, 422, err.Code)
	})

	t.Run("patch with 101 ops returns error", func(t *testing.T) {
		var ops []string
		for range 101 {
			ops = append(ops, `{"op":"replace","path":"/name","value":"bar"}`)
		}

		entity := sampleEntity()
		body := []byte("[" + strings.Join(ops, ",") + "]")

		_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, body)
		assert.Equal(t, 422, err.Code)
	})

	t.Run("path longer than 255 chars returns error", func(t *testing.T) {
		longPath := "/" + strings.Repeat("x", 255)
		entity := sampleEntity()
		body := []byte(`[{"op":"replace","path":"` + longPath + `","value":"bar"}]`)

		_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, body)
		assert.Equal(t, 422, err.Code)
	})

	t.Run("path exactly 255 chars is valid", func(t *testing.T) {
		okPath := "/" + strings.Repeat("x", 254)
		entity := sampleEntity()
		body := []byte(`[{"op":"add","path":"` + okPath + `","value":"bar"}]`)

		_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, body)
		assert.Nil(t, err)
	})
}
