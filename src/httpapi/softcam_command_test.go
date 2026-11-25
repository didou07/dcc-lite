package httpapi

import (
	"os/exec"
	"strings"
	"testing"
)

func TestSoftcamCommandsAreSingleLineAndShellValid(t *testing.T) {
	commands := map[string]string{
		"info":       softcamCommandLine(softcamInfoCommand),
		"start":      mustSoftcamAction(t, "start", "softcam.oscam"),
		"stop":       mustSoftcamAction(t, "stop", "softcam.oscam"),
		"restart":    mustSoftcamAction(t, "restart", "softcam.oscam"),
		"switch":     mustSoftcamAction(t, "switch", "softcam.oscam"),
		"kill":       softcamCommandLine(mustSoftcamKill(t, []int{733, 811})),
		"kill-all":   softcamCommandLine(softcamKillAllCommand()),
		"log-first":  softcamCommandLine(softcamLogCommand("/tmp/oscam.log", 200, -1)),
		"log-follow": softcamCommandLine(softcamLogCommand("/tmp/oscam.log", 200, 4096)),
	}

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Log("shell parser unavailable; checking Telnet input constraints only")
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			if strings.ContainsAny(command, "\r\n\x00") {
				t.Fatalf("command contains a forbidden control character")
			}
			if len(command) > 6000 {
				t.Fatalf("command exceeds Telnet input limit: %d bytes", len(command))
			}
			if sh != "" {
				if output, err := exec.Command(sh, "-n", "-c", command).CombinedOutput(); err != nil {
					t.Fatalf("invalid shell command: %v: %s", err, output)
				}
			}
		})
	}
}

func TestSoftcamInitActionsStayIndependentFromProcessScan(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart", "switch"} {
		command := mustSoftcamAction(t, action, "softcam.ncam")
		if strings.Contains(command, "@@ps") || strings.Contains(command, "softcam_is_cam") {
			t.Fatalf("%s action unexpectedly depends on process scanning: %s", action, command)
		}
		if strings.ContainsAny(command, "\r\n\x00") {
			t.Fatalf("%s action is not a single Telnet command", action)
		}
	}
	switchCommand := mustSoftcamAction(t, "switch", "softcam.ncam")
	for _, required := range []string{"/etc/init.d/softcam", "/etc/init.d/softcam.ncam", "ln -sf", `"$A" stop`, `"$A" start`} {
		if !strings.Contains(switchCommand, required) {
			t.Errorf("switch command is missing %q: %s", required, switchCommand)
		}
	}
}

func TestSoftcamCommandLineKeepsAwkStatementSeparators(t *testing.T) {
	command := softcamCommandLine("awk '\n  { first=1\n    second=2\n  }\n  /value/ { print first, second }\n' /tmp/config")
	if strings.ContainsAny(command, "\r\n") {
		t.Fatalf("normalized awk command still contains a line break: %q", command)
	}
	if !strings.Contains(command, "{ first=1; second=2 }") {
		t.Fatalf("normalized awk statements lost their separators: %q", command)
	}
}

func mustSoftcamAction(t *testing.T, action, cam string) string {
	t.Helper()
	command, err := softcamActionCommand(action, cam)
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func mustSoftcamKill(t *testing.T, pids []int) string {
	t.Helper()
	command, err := softcamKillCommand(pids)
	if err != nil {
		t.Fatal(err)
	}
	return command
}
