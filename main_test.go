package main

import (
	"net/http"
	"strings"
	"testing"

	"grok-inspection/cpasdk/pluginapi"
)

func TestManagementStatusReturnsJSON(t *testing.T) {
	response := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/management/plugins/grok-inspection/status",
	})

	if got := response.Headers.Get("content-type"); !strings.Contains(got, "application/json") {
		t.Fatalf("content-type = %q, want application/json", got)
	}
}

func TestResourceStatusReturnsHTML(t *testing.T) {
	response := dispatchManagement(pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/grok-inspection/status",
	})

	if got := response.Headers.Get("content-type"); !strings.Contains(got, "text/html") {
		t.Fatalf("content-type = %q, want text/html", got)
	}
}

func TestResourcePageDoesNotPollWithoutManagementKey(t *testing.T) {
	page := string(renderUIPage(pluginName))
	guard := "if (!keyInput.value.trim())"
	refresh := "async function refresh()"
	refreshIndex := strings.Index(page, refresh)
	guardIndex := strings.Index(page, guard)

	if refreshIndex < 0 || guardIndex < refreshIndex {
		t.Fatalf("refresh must guard management requests with %q", guard)
	}
}
