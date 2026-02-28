/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/stretchr/testify/require"
)

var (
	specOnce sync.Once
	specDoc  *openapi3.T
	specErr  error
)

// loadSpec loads and validates the OpenAPI spec once per test binary.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	specOnce.Do(func() {
		_, filename, _, _ := runtime.Caller(0)
		specPath := filepath.Join(filepath.Dir(filename), "..", "..", "api", "openapi.yaml")
		loader := openapi3.NewLoader()
		specDoc, specErr = loader.LoadFromFile(specPath)
		if specErr != nil {
			return
		}
		specErr = specDoc.Validate(context.Background())
	})
	require.NoError(t, specErr, "OpenAPI spec must be valid")
	require.NotNil(t, specDoc)
	return specDoc
}

// validateResponse checks that an httptest.ResponseRecorder's output
// matches the OpenAPI spec for the given request.
func validateResponse(t *testing.T, doc *openapi3.T, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()

	router, err := gorillamux.NewRouter(doc)
	require.NoError(t, err, "failed to create OpenAPI router")

	route, pathParams, err := router.FindRoute(req)
	require.NoError(t, err, "request %s %s not found in OpenAPI spec", req.Method, req.URL.Path)

	responseInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status: rec.Code,
		Header: rec.Header(),
		Body:   io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
	}

	err = openapi3filter.ValidateResponse(context.Background(), responseInput)
	require.NoError(t, err, "response for %s %s (status %d) does not match OpenAPI spec", req.Method, req.URL.Path, rec.Code)
}

func TestOpenAPISpecIsValid(t *testing.T) {
	loadSpec(t)
}
