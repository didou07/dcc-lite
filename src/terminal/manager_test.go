package terminal

import (
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestShellQuotePreservesPathCharacters(t *testing.T) {
	for _, test := range []struct {
		value string
		want  string
	}{
		{value: "/media/hdd/movie", want: "'/media/hdd/movie'"},
		{value: "/media/receiver's files", want: "'/media/receiver'\\''s files'"},
		{value: "folder with spaces", want: "'folder with spaces'"},
	} {
		if got := shellQuote(test.value); got != test.want {
			t.Errorf("shellQuote(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestInitialDirectoryAndCommandWaitsForTelnetLogin(t *testing.T) {
	client, peer := net.Pipe()
	manager := NewManager()
	defer manager.CloseAll()
	telnet := NewTelnetSession(client, 100, 30, "root", "dreambox")
	manager.mu.Lock()
	manager.sessions["test-session"] = &managedSession{backend: telnet}
	manager.mu.Unlock()

	result := make(chan error, 1)
	go func() {
		defer peer.Close()
		if _, err := peer.Write([]byte("login:")); err != nil {
			result <- err
			return
		}
		username := make([]byte, len("root\r\n"))
		if _, err := io.ReadFull(peer, username); err != nil {
			result <- err
			return
		}
		if string(username) != "root\r\n" {
			result <- fmt.Errorf("unexpected username response %q", username)
			return
		}
		if _, err := peer.Write([]byte("Password:")); err != nil {
			result <- err
			return
		}
		password := make([]byte, len("dreambox\r\n"))
		if _, err := io.ReadFull(peer, password); err != nil {
			result <- err
			return
		}
		if string(password) != "dreambox\r\n" {
			result <- fmt.Errorf("unexpected password response %q", password)
			return
		}
		if _, err := peer.Write([]byte("root@receiver:~# ")); err != nil {
			result <- err
			return
		}
		command := make([]byte, len("cd '/etc/receiver'\\''s files' && ls -l\r"))
		if _, err := io.ReadFull(peer, command); err != nil {
			result <- err
			return
		}
		want := "cd '/etc/receiver'\\''s files' && ls -l\r"
		if string(command) != want {
			result <- fmt.Errorf("initial command = %q, want %q", command, want)
			return
		}
		result <- nil
	}()

	readDone := make(chan error, 1)
	go func() {
		buffer := make([]byte, 1024)
		for {
			if _, err := telnet.Read(buffer); err != nil {
				readDone <- err
				return
			}
		}
	}()

	if err := manager.SetInitialDirectoryAndCommand("test-session", "/etc/receiver's files", "ls -l"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Telnet initial command was not delivered after login")
	}
	_ = peer.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Telnet reader did not stop")
	}
}

func TestInitialCommandRejectsControlCharacters(t *testing.T) {
	manager := NewManager()
	defer manager.CloseAll()
	for _, command := range []string{"echo ok\nwhoami", "echo ok\rwhoami", "echo ok\x00whoami"} {
		if err := manager.SetInitialDirectoryAndCommand("missing", "/tmp", command); err == nil {
			t.Fatalf("expected control characters to be rejected: %q", command)
		}
	}
}

func TestValidateTerminalSizeAllowsNarrowScreens(t *testing.T) {
	for _, size := range [][2]int{{20, 5}, {26, 12}, {80, 24}, {240, 100}} {
		if err := ValidateDimensions(size[0], size[1]); err != nil {
			t.Errorf("valid size %dx%d rejected: %v", size[0], size[1], err)
		}
	}
	for _, size := range [][2]int{{19, 10}, {20, 4}, {241, 24}, {80, 101}} {
		if err := ValidateDimensions(size[0], size[1]); err == nil {
			t.Errorf("invalid size %dx%d accepted", size[0], size[1])
		}
	}
}
