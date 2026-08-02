package graph

import "testing"

func TestPackageScopeMatch(t *testing.T) {
	cases := []struct {
		packageID string
		path      string
		filter    string
		want      bool
	}{
		{"package:@acme/auth", "packages/auth/src/session.ts", "auth", true},
		{"package:@acme/auth", "packages/auth/src/session.ts", "@acme/auth", true},
		{"package:@acme/auth", "packages/auth/src/session.ts", "package:@acme/auth", true},
		{"package:@acme/billing", "packages/billing/src/x.ts", "auth", false},
		{"", "apps/web/src/page.tsx", "web", true},
		{"", "packages/core/lib.ts", "billing", false},
		{"package:core", "src/x.ts", "", true},
	}
	for _, tc := range cases {
		if got := PackageScopeMatch(tc.packageID, tc.path, tc.filter); got != tc.want {
			t.Fatalf("PackageScopeMatch(%q,%q,%q)=%v want %v", tc.packageID, tc.path, tc.filter, got, tc.want)
		}
	}
}
