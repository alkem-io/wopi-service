package model

import (
	"testing"
	"time"
)

func TestAccessToken_IsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"future expiry", time.Now().Add(1 * time.Hour), false},
		{"past expiry", time.Now().Add(-1 * time.Hour), true},
		{"just expired", time.Now().Add(-1 * time.Millisecond), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := AccessToken{ExpiresAt: tt.expiresAt}
			if got := token.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAccessToken_HasPermission(t *testing.T) {
	tests := []struct {
		name        string
		permissions string
		perm        string
		want        bool
	}{
		{"exact match read", "read", "read", true},
		{"exact match write", "write", "write", true},
		{"comma-separated has read", "read,write", "read", true},
		{"comma-separated has write", "read,write", "write", true},
		{"not present", "read", "write", false},
		{"empty permissions", "", "read", false},
		{"partial match should fail", "readonly", "read", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := AccessToken{Permissions: tt.permissions}
			if got := token.HasPermission(tt.perm); got != tt.want {
				t.Errorf("HasPermission(%q) = %v, want %v", tt.perm, got, tt.want)
			}
		})
	}
}
