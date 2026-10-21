package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// time.Local is initialized from TZ once per process, so the check runs in a
// child process with the production environment instead of mutating it here.
func TestTimezoneCheckUsesAsiaShanghai(t *testing.T) {
	if os.Getenv("XBT_TZ_PROBE") == "1" {
		checkTimezone()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestTimezoneCheckUsesAsiaShanghai$")
	cmd.Env = append(os.Environ(), "XBT_TZ_PROBE=1", "TZ=Asia/Shanghai", "ZONEINFO=/nonexistent-zoneinfo")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("timezone probe failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "timezone check: status=ok local=Asia/Shanghai") {
		t.Fatalf("timezone check degraded or missing:\n%s", out)
	}
}
