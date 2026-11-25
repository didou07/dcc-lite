//go:build !windows

package updater

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fastRun(e *testEnv) {
	e.u.opt.firstCheck = 10 * time.Millisecond
	e.u.opt.every = 20 * time.Millisecond
}

func runAsync(e *testEnv, t *testing.T) (stop func()) {
	c, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.u.Run(c); close(done) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not stop")
		}
	}
}

func TestRunNotifiesButNeverInstalls(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	fastRun(e)
	e.u.opt.Idle = func() bool { return true }
	stop := runAsync(e, t)
	defer stop()
	deadline := time.Now().Add(5 * time.Second)
	for !e.u.Status().Available {
		if time.Now().After(deadline) {
			t.Fatal("the new version was not noticed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // many more check cycles
	select {
	case <-e.u.Restart():
		t.Fatal("an update was installed without being asked for")
	default:
	}
	if e.exeContents(t) != oldBinary {
		t.Fatal("program replaced without being asked")
	}
	if !e.u.Status().Notify {
		t.Fatal("notices should start enabled")
	}
}

func TestRunDoesNotLookForUpdatesWhenNoticesAreOff(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	if err := e.u.SetNotify(false); err != nil {
		t.Fatal(err)
	}
	fastRun(e)
	stop := runAsync(e, t)
	time.Sleep(300 * time.Millisecond)
	stop()
	if st := e.u.Status(); st.Available || !st.CheckedAt.IsZero() {
		t.Fatalf("it still checked: %+v", st)
	}
	// A check the person asks for still works.
	if st, err := e.u.Check(ctx(t)); err != nil || !st.Available {
		t.Fatalf("manual check: %+v %v", st, err)
	}
}

func TestInstallIsRefusedWhileTransfersAreActive(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	if _, err := e.u.Check(ctx(t)); err != nil {
		t.Fatal(err)
	}
	e.u.opt.Idle = func() bool { return false }
	if err := e.u.Install(ctx(t)); !errors.Is(err, ErrActive) {
		t.Fatalf("err = %v", err)
	}
	if e.exeContents(t) != oldBinary {
		t.Fatal("program replaced while busy")
	}
	e.u.opt.Idle = func() bool { return true }
	if err := e.u.Install(ctx(t)); err != nil {
		t.Fatalf("install after becoming idle: %v", err)
	}
}
