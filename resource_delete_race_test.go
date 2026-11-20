package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createResource sends POST path with body and returns the id of the
// created resource.
func createResource(t *testing.T, path, body string) string {
	t.Helper()

	req, err := newTestRequest("POST", path, strings.NewReader(body))
	require.NoError(t, err)
	req.Header = map[string][]string{
		"Authorization": {goodToken},
		"Content-Type":  {"application/json"},
	}

	res, err := app.Test(req, -1)
	require.NoError(t, err)
	require.Equal(t, 200, res.StatusCode)

	var created map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))

	return created["id"].(string)
}

func TestResourceDeleteDuringLogOrWebhookCreate(t *testing.T) {
	resources := []struct {
		name string
		path string
		body func(round int) string
	}{
		{
			name: "software",
			path: "/v1/software",
			body: func(round int) string {
				return fmt.Sprintf(`{"url": "https://example.org/resource-delete-race/%d", "publiccodeYml": "-"}`, round)
			},
		},
		{
			name: "publisher",
			path: "/v1/publishers",
			body: func(round int) string {
				return fmt.Sprintf(`{"description": "Resource delete race %d", `+
					`"codeHosting": [{"url": "https://example.org/resource-delete-race-%d"}]}`, round, round)
			},
		},
	}

	children := []struct {
		name        string
		path        string
		body        string
		table       string
		softDeletes bool
	}{
		{name: "log", path: "logs", body: `{"message": "delete race"}`, table: "logs", softDeletes: true},
		{name: "webhook", path: "webhooks", body: `{"url": "https://example.org/delete-race-hook"}`, table: "webhooks"},
	}

	for _, resource := range resources {
		for _, child := range children {
			t.Run(resource.name+" "+child.name, func(t *testing.T) {
				loadFixtures(t)

				for round := range catalogDeleteRaceRounds {
					id := createResource(t, resource.path, resource.body(round))

					start := make(chan struct{})
					created := make(chan int, 1)
					deleted := make(chan int, 1)

					go func() {
						<-start
						created <- sendAuthorized("POST", resource.path+"/"+id+"/"+child.path, child.body)
					}()

					go func() {
						<-start
						deleted <- sendAuthorized("DELETE", resource.path+"/"+id, "")
					}()

					close(start)

					createCode, deleteCode := <-created, <-deleted

					assert.Contains(t, []int{200, 404}, createCode, "round %d", round)
					assert.Equal(t, 204, deleteCode, "round %d", round)
					assert.Equal(t, 0, liveRows(t, child.table, "entity_id", id, child.softDeletes),
						"round %d: %s left behind a deleted %s", round, child.name, resource.name)
				}
			})
		}
	}
}
