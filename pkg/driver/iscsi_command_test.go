package driver

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

func TestWithCommandOutput(t *testing.T) {
	_, commandErr := exec.Command("sh", "-c", "printf '  iscsiadm: command not found\\n' >&2; exit 127").Output()
	var exitErr *exec.ExitError
	if !errors.As(commandErr, &exitErr) {
		t.Fatalf("expected ExitError, got %v", commandErr)
	}

	for _, wrapped := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapped=%t", wrapped), func(t *testing.T) {
			err := commandErr
			if wrapped {
				err = fmt.Errorf("iscsi command failed: %w", err)
			}
			got := withCommandOutput(err)
			if want := err.Error() + ": iscsiadm: command not found"; got.Error() != want {
				t.Errorf("got %q, want %q", got.Error(), want)
			}
			if !errors.Is(got, err) {
				t.Error("original error was not preserved")
			}
			var gotExitErr *exec.ExitError
			if !errors.As(got, &gotExitErr) || gotExitErr.ExitCode() != 127 {
				t.Errorf("exit status was not preserved: %v", got)
			}
		})
	}
}

func TestWithCommandOutputUnchanged(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "nil"},
		{name: "ordinary error", err: errors.New("connection failed")},
		{name: "exit error without stderr", err: &exec.ExitError{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := withCommandOutput(tt.err); got != tt.err {
				t.Errorf("got %v, want original error %v", got, tt.err)
			}
		})
	}
}
