package terminal

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"testing"
)

func TestTelnetNegotiationAndPlainOutput(t *testing.T) {
	client, server := net.Pipe()
	session := NewTelnetSession(client, 100, 30)
	defer session.Close()
	serverResult := make(chan []byte, 1)
	go func() {
		_, _ = server.Write([]byte{telnetIAC, telnetWILL, telnetOptEcho, 'l', 'o', 'g', 'i', 'n', ':'})
		response := make([]byte, 3)
		_, _ = io.ReadFull(server, response)
		serverResult <- response
	}()
	buffer := make([]byte, 32)
	n, err := session.Read(buffer)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if got := string(buffer[:n]); got != "login:" {
		t.Fatalf("unexpected terminal output %q", got)
	}
	if response := <-serverResult; !bytes.Equal(response, []byte{telnetIAC, telnetDO, telnetOptEcho}) {
		t.Fatalf("unexpected Telnet negotiation response: %v", response)
	}
	_ = server.Close()
}

func TestTelnetEscapesIACOnWrite(t *testing.T) {
	client, server := net.Pipe()
	session := NewTelnetSession(client, 100, 30)
	defer session.Close()
	want := []byte{'a', telnetIAC, telnetIAC, 'b'}
	result := make(chan []byte, 1)
	go func() { data := make([]byte, len(want)); _, _ = io.ReadFull(server, data); result <- data }()
	if _, err := session.Write([]byte{'a', telnetIAC, 'b'}); err != nil {
		t.Fatal(err)
	}
	if got := <-result; !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	_ = server.Close()
}

func TestTelnetNAWSNegotiationAndResize(t *testing.T) {
	client, server := net.Pipe()
	session := NewTelnetSession(client, 100, 30)
	defer session.Close()
	result := make(chan []byte, 1)
	go func() {
		_, _ = server.Write([]byte{telnetIAC, telnetDO, telnetOptNAWS})
		response := make([]byte, 12)
		_, _ = io.ReadFull(server, response)
		result <- response
	}()
	buffer := make([]byte, 1)
	readDone := make(chan error, 1)
	go func() { _, err := session.Read(buffer); readDone <- err }()
	response := <-result
	want := []byte{telnetIAC, telnetWILL, telnetOptNAWS, telnetIAC, telnetSB, telnetOptNAWS, 0, 100, 0, 30, telnetIAC, telnetSE}
	if !bytes.Equal(response, want) {
		t.Fatalf("unexpected NAWS negotiation: %v", response)
	}
	_ = server.Close()
	<-readDone
}

func TestTelnetAutoLoginUsesProfileCredentials(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	session := NewTelnetSession(client, 100, 30, "root", "dreambox")
	defer session.Close()

	serverResult := make(chan error, 1)
	go func() {
		if _, err := server.Write([]byte("receiver login:")); err != nil {
			serverResult <- err
			return
		}
		username := make([]byte, len("root\r\n"))
		if _, err := io.ReadFull(server, username); err != nil {
			serverResult <- err
			return
		}
		if string(username) != "root\r\n" {
			serverResult <- fmt.Errorf("unexpected username response %q", username)
			return
		}
		if _, err := server.Write([]byte("Password:")); err != nil {
			serverResult <- err
			return
		}
		password := make([]byte, len("dreambox\r\n"))
		if _, err := io.ReadFull(server, password); err != nil {
			serverResult <- err
			return
		}
		if string(password) != "dreambox\r\n" {
			serverResult <- fmt.Errorf("unexpected password response %q", password)
			return
		}
		_, err := server.Write([]byte("Welcome"))
		serverResult <- err
	}()

	buffer := make([]byte, 64)
	if _, err := session.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestTelnetInitialCommandWaitsForAuthenticatedShellPrompt(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	session := NewTelnetSession(client, 100, 30, "root", "dreambox")
	defer session.Close()
	command := []byte("cd '/var/receiver/'\\''special' && pwd\r")
	if err := session.SetInitialCommand(command); err != nil {
		t.Fatal(err)
	}

	serverResult := make(chan error, 1)
	go func() {
		if _, err := server.Write([]byte("login:")); err != nil {
			serverResult <- err
			return
		}
		username := make([]byte, len("root\r\n"))
		if _, err := io.ReadFull(server, username); err != nil {
			serverResult <- err
			return
		}
		if _, err := server.Write([]byte("Password:")); err != nil {
			serverResult <- err
			return
		}
		password := make([]byte, len("dreambox\r\n"))
		if _, err := io.ReadFull(server, password); err != nil {
			serverResult <- err
			return
		}
		if _, err := server.Write([]byte("root@receiver:~# ")); err != nil {
			serverResult <- err
			return
		}
		got := make([]byte, len(command))
		if _, err := io.ReadFull(server, got); err != nil {
			serverResult <- err
			return
		}
		if string(got) != string(command) {
			serverResult <- fmt.Errorf("unexpected initial command %q", got)
			return
		}
		serverResult <- nil
	}()

	buffer := make([]byte, 64)
	for i := 0; i < 3; i++ {
		if _, err := session.Read(buffer); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}
