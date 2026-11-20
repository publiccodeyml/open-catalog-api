package common_test

import (
	"strings"
	"testing"

	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/jsondecoder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		entity := map[string]string{strings.TrimPrefix(okPath, "/"): "foo"}
		body := []byte(`[{"op":"add","path":"` + okPath + `","value":"bar"}]`)

		_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, body)
		assert.Nil(t, err)
	})
}

func TestApplyPatchLimitsCopiedBytes(t *testing.T) {
	entity := sampleEntity()
	body := "[" + strings.TrimSuffix(strings.Repeat(
		`{"op":"copy","from":"/aliases","path":"/aliases/-"},`, 100), ",") + "]"

	_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, []byte(body))
	require.NotNil(t, err)
	assert.Equal(t, 422, err.Code)
	assert.EqualError(t, err, "copy operations must not add more than 4194304 bytes")
	assert.Equal(t, sampleEntity(), entity)
}

func TestApplyPatchRejectsMalformedMergePatch(t *testing.T) {
	entity := sampleEntity()

	_, err := common.ApplyPatch(&entity, "application/merge-patch+json", []byte(`{"name":`))

	assert.Equal(t, 400, err.Code)
}

func TestApplyPatchMediaType(t *testing.T) {
	for _, contentType := range []string{
		common.ContentTypeJSONPatch,
		"application/json-patch+json; charset=utf-8",
		"Application/JSON-Patch+JSON",
		`Application/JSON-Patch+JSON; charset="UTF-8"`,
	} {
		t.Run(contentType, func(t *testing.T) {
			entity := sampleEntity()
			updated, err := common.ApplyPatch(&entity, contentType,
				[]byte(`[{"op":"replace","path":"/name","value":"changed"}]`))
			assert.Nil(t, err)
			assert.Equal(t, "changed", updated.Name)
		})
	}
}

func TestApplyPatchRejectsWrongValueTypes(t *testing.T) {
	for _, body := range []string{
		`[{"op":"replace","path":"/name","value":42}]`,
		`[{"op":"replace","path":"/active","value":"true"}]`,
		`[{"op":"replace","path":"/aliases/0","value":false}]`,
		`[{"op":"add","path":"/aliases/-","value":{}}]`,
	} {
		t.Run(body, func(t *testing.T) {
			entity := sampleEntity()
			_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, []byte(body))
			require.NotNil(t, err)
			assert.Equal(t, 422, err.Code)
			assert.Equal(t, sampleEntity(), entity)
		})
	}
}

func TestApplyPatchEncodingFailure(t *testing.T) {
	entity := make(chan int)
	_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch,
		[]byte(`[{"op":"add","path":"/name","value":"bar"}]`))
	require.NotNil(t, err)
	assert.Equal(t, 500, err.Code)
}

func TestApplyPatchRejectsUnknownMembers(t *testing.T) {
	for _, body := range []string{
		`[{"op":"add","path":"/nmae","value":"bar"}]`,
		`[{"op":"add","path":"/Name","value":"bar"}]`,
		`[{"op":"add","path":"/ID","value":"evil"}]`,
		`[{"op":"add","path":"/CreatedAt","value":"2000"}]`,
		`[{"op":"add","path":"/nested/typo","value":"bar"}]`,
		`[{"op":"add","path":"/nested/Name","value":"bar"}]`,
		`[{"op":"copy","from":"/name","path":"/nmae"}]`,
		`[{"op":"replace","path":"/nested","value":{"name":"bar","typo":true}}]`,
	} {
		t.Run(body, func(t *testing.T) {
			type nested struct {
				Name string `json:"name"`
			}
			entity := struct {
				ID        string `json:"id"`
				CreatedAt string `json:"createdAt"`
				Name      string `json:"name"`
				Nested    nested `json:"nested"`
			}{ID: "one", CreatedAt: "2026", Name: "foo", Nested: nested{Name: "foo"}}
			original := entity
			_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, []byte(body))
			require.NotNil(t, err)
			assert.Equal(t, 422, err.Code)
			assert.ErrorIs(t, err, jsondecoder.ErrUnknownField)
			assert.Equal(t, original, entity)
		})
	}
}

func TestApplyPatchRecoversFromLibraryPanics(t *testing.T) {
	for _, value := range []string{`[null]`, `["a", null]`, `[null, null]`, `[[null]]`} {
		t.Run(value, func(t *testing.T) {
			entity := sampleEntity()
			body := []byte(`[{"op":"test","path":"/aliases","value":` + value + `}]`)

			_, err := common.ApplyPatch(&entity, common.ContentTypeJSONPatch, body)
			require.NotNil(t, err)
			assert.Equal(t, 422, err.Code)
		})
	}
}
