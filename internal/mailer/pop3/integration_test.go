//go:build integration

// Fake POP3 server for integration testing (see PLAN.md §6.2): the
// Go ecosystem has no common in-process POP3 test server, so this
// implements just enough of RFC 1939 (USER/PASS/NOOP/UIDL/TOP/QUIT)
// to exercise internal/mailer/pop3.Client without a real mailbox.
package pop3

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"xmail/internal/account"
)

type fakePop3Message struct {
	uid     string
	subject string
	from    string
	to      string
	date    string
}

var fakeMailbox = []fakePop3Message{
	{uid: "uid-1", subject: "Oldest", from: "a@example.com", to: "me@example.com", date: "Mon, 01 Jan 2024 00:00:00 +0000"},
	{uid: "uid-2", subject: "Middle", from: "b@example.com", to: "me@example.com", date: "Tue, 02 Jan 2024 00:00:00 +0000"},
	{uid: "uid-3", subject: "Newest", from: "c@example.com", to: "me@example.com", date: "Wed, 03 Jan 2024 00:00:00 +0000"},
}

const (
	fakeUsername = "testuser"
	fakePassword = "testpass"
)

func startFakePop3Server(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleFakePop3Conn(conn)
		}
	}()

	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err = strconv.Atoi(p)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return h, port
}

func handleFakePop3Conn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	write := func(s string) {
		w.WriteString(s + "\r\n")
		w.Flush()
	}

	write("+OK fake pop3 server ready")

	var user string
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		cmd := strings.ToUpper(fields[0])

		switch cmd {
		case "USER":
			if len(fields) > 1 {
				user = fields[1]
			}
			write("+OK")
		case "PASS":
			pass := ""
			if len(fields) > 1 {
				pass = fields[1]
			}
			if user == fakeUsername && pass == fakePassword {
				write("+OK")
			} else {
				write("-ERR invalid credentials")
			}
		case "NOOP":
			write("+OK")
		case "UIDL":
			write("+OK")
			for i, m := range fakeMailbox {
				w.WriteString(fmt.Sprintf("%d %s\r\n", i+1, m.uid))
			}
			write(".")
		case "TOP":
			if len(fields) < 2 {
				write("-ERR missing message id")
				continue
			}
			id, err := strconv.Atoi(fields[1])
			if err != nil || id < 1 || id > len(fakeMailbox) {
				write("-ERR no such message")
				continue
			}
			m := fakeMailbox[id-1]
			write("+OK")
			w.WriteString(fmt.Sprintf("Subject: %s\r\n", m.subject))
			w.WriteString(fmt.Sprintf("From: %s\r\n", m.from))
			w.WriteString(fmt.Sprintf("To: %s\r\n", m.to))
			w.WriteString(fmt.Sprintf("Date: %s\r\n", m.date))
			w.WriteString("\r\n")
			write(".")
		case "QUIT":
			write("+OK bye")
			return
		default:
			write("-ERR unknown command")
		}
	}
}

func TestIntegration_TestConnection_POP3(t *testing.T) {
	host, port := startFakePop3Server(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, fakeUsername, fakePassword)
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
}

func TestIntegration_TestConnection_POP3_WrongPassword(t *testing.T) {
	host, port := startFakePop3Server(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, fakeUsername, "wrong")
	if err := c.TestConnection(context.Background()); err == nil {
		t.Fatal("TestConnection() error = nil, want error for wrong password")
	}
}

func TestIntegration_Fetch_POP3(t *testing.T) {
	host, port := startFakePop3Server(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, fakeUsername, fakePassword)

	msgs, err := c.Fetch(context.Background(), "", 2, 0)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Fetch() returned %d messages, want 2", len(msgs))
	}
	if msgs[0].Subject != "Newest" || msgs[1].Subject != "Middle" {
		t.Errorf("Fetch() order = [%q, %q], want [Newest, Middle]", msgs[0].Subject, msgs[1].Subject)
	}
	if msgs[0].UID != "uid-3" {
		t.Errorf("msgs[0].UID = %q, want %q", msgs[0].UID, "uid-3")
	}
	if msgs[0].From != "c@example.com" {
		t.Errorf("msgs[0].From = %q, want %q", msgs[0].From, "c@example.com")
	}
}

func TestIntegration_Fetch_POP3_Offset(t *testing.T) {
	host, port := startFakePop3Server(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, fakeUsername, fakePassword)

	msgs, err := c.Fetch(context.Background(), "", 10, 2)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(msgs) != 1 || msgs[0].Subject != "Oldest" {
		t.Fatalf("Fetch(offset=2) = %v, want just [Oldest]", msgs)
	}
}

func TestStartTLS_NotSupported(t *testing.T) {
	c := New(account.ConnectionConfig{Host: "example.com", Port: 995, TLSMode: account.TLSModeStartTLS}, "u", "p")
	if err := c.TestConnection(context.Background()); err == nil {
		t.Fatal("TestConnection() with starttls error = nil, want explicit unsupported error")
	}
}
