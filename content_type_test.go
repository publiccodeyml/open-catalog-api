package main

import (
	"testing"
)

func TestWriteEndpointsRejectNonJSONBodies(t *testing.T) {
	logProblem := `{"title":"can't create Log","detail":"Content-Type must be a JSON media type","status":415}`

	tests := []TestCase{
		{
			description: "POST log with an XML body",
			query:       "POST /v1/logs",
			body:        `<Log><Message>xml</Message><Bogus>1</Bogus></Log>`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/xml"},
			},
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        logProblem,
		},
		{
			description: "POST log with a form body",
			query:       "POST /v1/logs",
			body:        `message=form&bogus=1`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/x-www-form-urlencoded"},
			},
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        logProblem,
		},
		{
			description: "POST log with a multipart body",
			query:       "POST /v1/logs",
			body:        "--b\r\nContent-Disposition: form-data; name=\"message\"\r\n\r\nmultipart\r\n--b--\r\n",
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"multipart/form-data; boundary=b"},
			},
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        logProblem,
		},
		{
			description: "POST log with a malformed Content-Type",
			query:       "POST /v1/logs",
			body:        `{"message": "j"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"json"},
			},
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        logProblem,
		},
		{
			description: "POST log without a Content-Type",
			query:       "POST /v1/logs",
			body:        `{"message": "j"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
			},
			expectedCode:        400,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Log","detail":"invalid or malformed JSON","status":400}`,
		},
		{
			description: "POST log with a JSON Content-Type and parameters",
			query:       "POST /v1/logs",
			body:        `{"message": "j", "bogus": 1}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"Application/JSON; charset=utf-8"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Log","detail":"unknown field in JSON input","status":422}`,
		},
		{
			description: "POST catalog with an XML body",
			query:       "POST /v1/catalogs",
			body:        `<Catalog><Name>xml</Name><Id>xml</Id></Catalog>`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/xml"},
			},
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Catalog","detail":"Content-Type must be a JSON media type","status":415}`,
		},
		{
			description: "PATCH publisher with a form body",
			query:       "PATCH /v1/publishers/" + rootPublisherID,
			body:        `description=form`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/x-www-form-urlencoded"},
			},
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Publisher","detail":"Content-Type must be a JSON media type","status":415}`,
		},
		{
			description: "PATCH software analysis with an XML body",
			query:       "PATCH /v1/software/" + rootSoftwareID + "/analysis",
			body:        `<badges><v>1</v></badges>`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/xml"},
			},
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Software analysis","detail":"Content-Type must be a JSON media type","status":415}`,
		},
	}

	runTestCases(t, tests)
}
