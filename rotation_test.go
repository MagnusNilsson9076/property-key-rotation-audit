package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRetryReconstructsBody(t *testing.T) {
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		bodies = append(bodies, body["name"].(string))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"ok":false,"error":{"code":"RATE_LIMITED"}}`))
			return
		}
		w.Write([]byte(`{"ok":true,"data":{"key_id":"key-1"}}`))
	}))
	defer server.Close()
	c := &InfraiClient{baseURL: server.URL, http: server.Client()}
	if _, err := c.CreateTemporaryKey(context.Background(), "temporary"); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != "temporary" || bodies[1] != "temporary" {
		t.Fatalf("retry bodies: %v", bodies)
	}
}

func TestRotateAndAuditRevokesOnSearchFailure(t *testing.T) {
	var revoked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/account/keys/create":
			w.Write([]byte(`{"ok":true,"data":{"key_id":"key-1"}}`))
		case strings.HasPrefix(r.URL.Path, "/v1/account/keys/rotate/"):
			w.Write([]byte(`{"ok":true}`))
		case r.URL.Path == "/v1/logs/search":
			if r.URL.Query().Get("q") != "deployment key=key-1" || r.URL.Query().Has("query") {
				t.Errorf("invalid search query: %s", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"ok":false,"error":{"code":"SEARCH_FAILED"}}`))
		case r.URL.Path == "/v1/account/keys/revoke/key-1":
			revoked = true
			w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := &InfraiClient{baseURL: server.URL, http: server.Client()}
	in := RotationInput{Maintenance: MaintenanceRequest{ID: "m", Property: "p", Summary: "s"}, Document: TenantDocument{TenantID: "t", Name: "n", Verified: true}, Reminder: InspectionReminder{Property: "p", DueOn: "tomorrow"}, GraceHours: 1}
	_, err := c.RotateAndAudit(context.Background(), in)
	if err == nil || !revoked {
		t.Fatalf("error = %v, revoked = %v", err, revoked)
	}
}

func TestReadyForRotation(t *testing.T) {
	base := RotationInput{
		Maintenance: MaintenanceRequest{ID: "mr-19", Property: "Maple Court", Summary: "leak review"},
		Document:    TenantDocument{TenantID: "tenant-4", Name: "lease.pdf", Verified: true},
		Reminder:    InspectionReminder{Property: "Maple Court", DueOn: "2026-10-02"},
		GraceHours:  24,
	}
	cases := []struct {
		name string
		edit func(*RotationInput)
		want bool
	}{
		{name: "verified property record can rotate", edit: func(*RotationInput) {}, want: true},
		{name: "unverified document blocks rotation", edit: func(in *RotationInput) { in.Document.Verified = false }, want: false},
		{name: "wrong reminder property blocks rotation", edit: func(in *RotationInput) { in.Reminder.Property = "Pine House" }, want: false},
		{name: "no overlap blocks rotation", edit: func(in *RotationInput) { in.GraceHours = 0 }, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.edit(&in)
			err := readyForRotation(in)
			if (err == nil) != tc.want {
				t.Fatalf("readyForRotation() error = %v, want ready=%v", err, tc.want)
			}
		})
	}
}
