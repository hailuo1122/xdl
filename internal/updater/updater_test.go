package updater

import "testing"

func TestCompareVersions(t *testing.T) {
	if CompareVersions("0.0.5-m5", "0.0.4-m4") <= 0 {
		t.Fatal("expected newer")
	}
	if CompareVersions("1.0.0", "1.0.0") != 0 {
		t.Fatal("equal")
	}
	if CompareVersions("0.9.0", "1.0.0") >= 0 {
		t.Fatal("expected older")
	}
}

func TestParseStripsPrerelease(t *testing.T) {
	a := parseVer("1.2.3-beta")
	if len(a) < 3 || a[0] != 1 || a[1] != 2 || a[2] != 3 {
		t.Fatalf("%v", a)
	}
}
