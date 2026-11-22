package routes_test

import (
	"net/http/httptest"
	"github.com/L42ARO/sonic-screwdriver/compute-box/internal/routes"
	"testing"
)

func TestHealthDoesNotRequireAnAgentOrModelCall(t *testing.T) {
	response := httptest.NewRecorder()
	routes.New(routes.Deps{}).ServeHTTP(response, httptest.NewRequest("GET", "/healthz", nil))
	if response.Code != 200 || response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("unhealthy: %d %s", response.Code, response.Body.String())
	}
}
