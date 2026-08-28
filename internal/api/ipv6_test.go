// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bgovanlu/vnprox/internal/ipv6"
)

type fakeIPv6Service struct{}

func (fakeIPv6Service) Segments(context.Context) (ipv6.SegmentsResponse, error) {
	return ipv6.SegmentsResponse{Items: []ipv6.Segment{}, GeneratedAt: 1}, nil
}

func TestIPv6Segments_GetOK(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, IPv6: fakeIPv6Service{},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ipv6/segments", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ipv6/segments status = %d, body: %s", rec.Code, rec.Body.String())
	}
}

// TestIPv6Segments_ZeroWriteSurface is T-1404 acceptance criterion 7's API
// half: GET /ipv6/segments accepts no request body and no write verb of
// any kind exists on the route — every method other than GET is refused,
// never a mutation path in disguise.
func TestIPv6Segments_ZeroWriteSurface(t *testing.T) {
	r := NewRouter(Options{
		Version: "test", DistFS: testDistFS(), Logger: testLogger(),
		Auth: fakeAuth{authenticated: true}, IPv6: fakeIPv6Service{},
	})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, "/api/v1/ipv6/segments", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("%s /ipv6/segments = 200, want anything but 200 (no write verb should succeed)", method)
		}
	}
}
