package main

import "testing"

func TestPlatformAsset(t *testing.T) {
	tests := []struct {
		os, arch, want string
	}{
		{"darwin", "arm64", "cx-darwin-arm64"},
		{"darwin", "amd64", "cx-darwin-amd64"},
		{"linux", "amd64", "cx-linux-amd64"},
		{"linux", "arm64", "cx-linux-arm64"},
	}
	for _, tt := range tests {
		got, err := platformAsset(tt.os, tt.arch)
		if err != nil || got != tt.want {
			t.Fatalf("platformAsset(%q,%q) = %q, %v; want %q", tt.os, tt.arch, got, err, tt.want)
		}
	}
}

func TestPlatformAssetRejectsUnsupported(t *testing.T) {
	if _, err := platformAsset("windows", "amd64"); err == nil {
		t.Fatal("expected unsupported OS error")
	}
	if _, err := platformAsset("linux", "386"); err == nil {
		t.Fatal("expected unsupported arch error")
	}
}

func TestVerifySHA256(t *testing.T) {
	payload := []byte("hello")
	sums := []byte("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824  cx-darwin-arm64\n")
	if err := verifySHA256("cx-darwin-arm64", payload, sums); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256("cx-darwin-arm64", []byte("changed"), sums); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}
