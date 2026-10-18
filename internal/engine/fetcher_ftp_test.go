package engine

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadFTPResume(t *testing.T) {
	payload := []byte("abcdefghijklmnopqrstuvwxyz")
	addr := serveFTP(t, payload)

	dir := t.TempDir()
	final := filepath.Join(dir, "note.txt")
	part := PartPath(final)
	if err := os.WriteFile(part, payload[:8], 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := DownloadFTP(context.Background(), DownloadOptions{
		URL:       "ftp://anonymous:guest@127.0.0.1" + addr[strings.LastIndex(addr, ":"):] + "/note.txt",
		Dir:       dir,
		Name:      "note.txt",
		FinalPath: final,
		PartPath:  part,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q", got)
	}
}

func serveFTP(t *testing.T, payload []byte) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go handleFTP(c, payload)
		}
	}()
	return ln.Addr().String()
}

func handleFTP(c net.Conn, payload []byte) {
	defer c.Close()
	w := bufio.NewWriter(c)
	r := bufio.NewReader(c)
	send := func(line string) {
		fmt.Fprintf(w, "%s\r\n", line)
		w.Flush()
	}
	send("220 ready")
	var offset int
	var dataWait chan net.Conn
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		cmd, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(cmd) {
		case "USER":
			send("331 password")
		case "PASS":
			send("230 logged in")
		case "FEAT":
			send("502 unknown")
		case "TYPE":
			send("200 type")
		case "SIZE":
			send("213 " + strconv.Itoa(len(payload)))
		case "EPSV":
			dl, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				send("425 cannot listen")
				continue
			}
			dataWait = make(chan net.Conn, 1)
			go func() {
				dc, err := dl.Accept()
				dl.Close()
				if err == nil {
					dataWait <- dc
				}
			}()
			port := dl.Addr().(*net.TCPAddr).Port
			send(fmt.Sprintf("229 Entering Extended Passive Mode (|||%d|)", port))
		case "REST":
			offset, _ = strconv.Atoi(arg)
			send("350 restart")
		case "RETR":
			send("150 opening")
			if dataWait == nil {
				send("425 no data port")
				continue
			}
			dc := <-dataWait
			start := offset
			if start < 0 || start > len(payload) {
				start = 0
			}
			_, _ = dc.Write(payload[start:])
			dc.Close()
			send("226 done")
		case "QUIT":
			send("221 bye")
			return
		default:
			send("200 ok")
		}
	}
}
