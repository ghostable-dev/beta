package app

import (
	"strings"
	"testing"
)

func TestRenderGhostableBanner(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	banner := renderGhostableBanner()

	if !strings.Contains(banner, "\x1b[38;2;70;185;168m") {
		t.Fatal("banner should include the first gradient color")
	}
	if !strings.Contains(banner, "▗▄▄▖▗▖ ▗▖ ▗▄▖") {
		t.Fatal("banner should include the Ghostable wordmark")
	}
	if strings.Contains(banner, "   \n") {
		t.Fatal("banner should trim trailing spaces from visible lines")
	}
}

func TestRenderGhostableBannerHonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	banner := renderGhostableBanner()

	if strings.Contains(banner, "\x1b[") {
		t.Fatalf("banner should omit ANSI escapes when NO_COLOR is set:\n%s", banner)
	}
	if !strings.Contains(banner, "▗▄▄▖▗▖ ▗▖ ▗▄▖") {
		t.Fatal("banner should keep the Ghostable wordmark")
	}
}
