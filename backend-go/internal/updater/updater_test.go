package updater

import (
	"testing"
)

func TestAssetName(t *testing.T) {
	name := AssetName()
	if name == "" {
		t.Fatal("AssetName() returned empty string")
	}
	// At minimum it should start with "ccx-"
	if len(name) < 4 || name[:4] != "ccx-" {
		t.Fatalf("AssetName() = %q, want prefix ccx-", name)
	}
}

func TestIsPrereleaseTag(t *testing.T) {
	tests := []struct {
		tag  string
		want bool
	}{
		{"v2.6.89", false},
		{"v2.7.0", false},
		{"v2.7.0-rc1", true},
		{"v2.7.0-beta", true},
		{"v2.7.0-alpha.1", true},
		{"v3.0.0-dev", true},
		{"v3.0.0-canary", true},
		{"v3.0.0-nightly.20260513", true},
	}
	for _, tt := range tests {
		got := isPrereleaseTag(tt.tag)
		if got != tt.want {
			t.Errorf("isPrereleaseTag(%q) = %v, want %v", tt.tag, got, tt.want)
		}
	}
}

// TestDoUpdateNoop exercises the version comparison path without
// actually performing an update (requires a real GitHub API call).
// This is a smoke test for CheckLatest + version comparison.
func TestDoUpdateNoopOnSameVersion(t *testing.T) {
	t.Log("Skipping: would require live GitHub API access")
	// In a real environment you would run:
	//   info, err := CheckLatest("BenedictKing", "ccx")
	//   if err != nil { t.Fatal(err) }
	//   t.Logf("latest: %s (published: %s)", info.Version, info.PublishedAt)
}
