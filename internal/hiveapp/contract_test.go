package hiveapp_test

import (
	"context"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// api/hive-openapi.yaml must stay a valid document (the web client's types
// are generated from it) and every operation it lists must reach a handler
// on the hive: a path the mux does not know answers a plain-text 404, a
// known one answers JSON (401 without a token, 200 for /healthz).
func TestHiveOpenAPIDocumentMatchesTheMux(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "hive-openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("api/hive-openapi.yaml is not a valid OpenAPI document: %v", err)
	}
	f := newAPI(t)
	param := regexp.MustCompile(`\{[^}]+\}`)
	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			url := f.srv.URL + param.ReplaceAllString(path, "x")
			req, _ := http.NewRequest(method, url, nil)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
				t.Errorf("%s %s: documented but not served (status %d, %s)", method, path, res.StatusCode, res.Header.Get("Content-Type"))
			}
		}
	}
}
