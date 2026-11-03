package adb

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Entry is one line of the adb server's device list.
type Entry struct {
	Serial string
	// State is "device" once usable; also "unauthorized", "offline",
	// "authorizing", "connecting", "no permissions", "recovery"…
	State string
	// Product, Model and Device come from the long listing (-l).
	Product, Model, Device string
	TransportID            string
}

// Ready reports whether the device accepts commands.
func (e Entry) Ready() bool { return e.State == "device" }

// ServerAddr is the adb server's address, from ANDROID_ADB_SERVER_PORT.
func ServerAddr() string {
	port := 5037
	if p, err := strconv.Atoi(os.Getenv("ANDROID_ADB_SERVER_PORT")); err == nil && p > 0 {
		port = p
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// ParseDevices reads a device list in the long format of `adb devices -l`
// and host:track-devices-l, without its header.
func ParseDevices(payload string) []Entry {
	var list []Entry
	for _, line := range strings.Split(payload, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		e := Entry{Serial: f[0], State: f[1]}
		rest := f[2:]
		if e.State == "no" && len(rest) > 0 && rest[0] == "permissions" {
			e.State = "no permissions"
			rest = rest[1:]
		}
		for _, kv := range rest {
			k, v, ok := strings.Cut(kv, ":")
			if !ok {
				continue
			}
			switch k {
			case "product":
				e.Product = v
			case "model":
				e.Model = v
			case "device":
				e.Device = v
			case "transport_id":
				e.TransportID = v
			}
		}
		list = append(list, e)
	}
	return list
}

// Track reports the adb server's device list each time it changes, until
// ctx ends, starting (and restarting) the server with client when it is
// not running. It returns ctx.Err().
func (c *Client) Track(ctx context.Context, report func([]Entry)) error {
	backoff := 250 * time.Millisecond
	for {
		err := c.trackOnce(ctx, report)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The server went away (adb kill-server, another adb of another
		// version replaced it…) or never ran: start ours, then listen
		// again.
		report(nil)
		_ = err
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_ = c.StartServer(sctx)
		cancel()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 4*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) trackOnce(ctx context.Context, report func([]Entry)) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", ServerAddr())
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := sendRequest(conn, "host:track-devices-l"); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	if err := readStatus(r); err != nil {
		return err
	}
	for {
		payload, err := readHexPayload(r)
		if err != nil {
			return err
		}
		report(ParseDevices(payload))
	}
}

// sendRequest writes a host protocol request: its length in four hex
// digits, then the request.
func sendRequest(w io.Writer, req string) error {
	_, err := fmt.Fprintf(w, "%04x%s", len(req), req)
	return err
}

func readStatus(r *bufio.Reader) error {
	status := make([]byte, 4)
	if _, err := io.ReadFull(r, status); err != nil {
		return err
	}
	switch string(status) {
	case "OKAY":
		return nil
	case "FAIL":
		msg, err := readHexPayload(r)
		if err != nil {
			return errors.New("adb server refused the request")
		}
		return errors.New(msg)
	default:
		return fmt.Errorf("adb server answered %q", status)
	}
}

func readHexPayload(r *bufio.Reader) (string, error) {
	head := make([]byte, 4)
	if _, err := io.ReadFull(r, head); err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(string(head), 16, 32)
	if err != nil {
		return "", fmt.Errorf("adb: bad length %q", head)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
