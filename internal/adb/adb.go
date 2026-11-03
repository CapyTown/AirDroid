// Package adb talks to Android devices through the adb executable and the
// adb server's host protocol.
//
// Long-lived queries, such as the list of devices, use the server's socket
// directly (host:track-devices-l), so that AirDroid learns about devices the
// moment they come and go without polling. One-shot commands run the adb
// executable, which also starts the server when it is not running.
package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client runs adb commands with one executable.
type Client struct {
	mu   sync.RWMutex
	path string
}

// New returns a client of the adb executable at path.
func New(path string) *Client { return &Client{path: path} }

// Path returns the adb executable in use.
func (c *Client) Path() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.path
}

// SetPath switches to another adb executable.
func (c *Client) SetPath(path string) {
	c.mu.Lock()
	c.path = path
	c.mu.Unlock()
}

// Command returns an exec.Cmd running adb with args.
func (c *Client) Command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path(), args...)
	cmd.Env = os.Environ()
	return cmd
}

// Run runs adb with args and returns its standard output, trimmed. A
// failure carries what adb printed.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := c.Command(ctx, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = out
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, &Error{Args: args, Msg: cleanMessage(msg), Err: err}
	}
	return out, nil
}

// Error is a failed adb command.
type Error struct {
	Args []string
	Msg  string
	Err  error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

func cleanMessage(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "adb: ")
	s = strings.TrimPrefix(s, "error: ")
	return s
}

// StartServer starts the adb server unless it runs.
func (c *Client) StartServer(ctx context.Context) error {
	_, err := c.Run(ctx, "start-server")
	return err
}

// KillServer stops the adb server.
func (c *Client) KillServer(ctx context.Context) error {
	_, err := c.Run(ctx, "kill-server")
	return err
}

var versionRe = regexp.MustCompile(`(?m)^Version ([^\s]+)`)
var bridgeVersionRe = regexp.MustCompile(`Android Debug Bridge version ([^\s]+)`)

// Version returns adb's release, as "36.0.0", or its bridge version.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.Run(ctx, "version")
	if err != nil {
		return "", err
	}
	return ParseVersion(out), nil
}

// ParseVersion reads the output of `adb version`.
func ParseVersion(out string) string {
	if m := versionRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	if m := bridgeVersionRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// Shell runs a shell command on the device and returns its output.
func (c *Client) Shell(ctx context.Context, serial string, command ...string) (string, error) {
	return c.Run(ctx, append([]string{"-s", serial, "shell"}, command...)...)
}

// Props returns the device's system properties.
func (c *Client) Props(ctx context.Context, serial string) (map[string]string, error) {
	out, err := c.Shell(ctx, serial, "getprop")
	if err != nil {
		return nil, err
	}
	return ParseGetprop(out), nil
}

var propRe = regexp.MustCompile(`^\[([^\]]+)\]: \[(.*)\]$`)

// ParseGetprop reads the output of `getprop`.
func ParseGetprop(out string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := propRe.FindStringSubmatch(line); m != nil {
			props[m[1]] = m[2]
		}
	}
	return props
}

// Connect connects to a device over TCP/IP, at host:port.
func (c *Client) Connect(ctx context.Context, addr string) error {
	out, err := c.Run(ctx, "connect", addr)
	if err != nil {
		return err
	}
	return connectResult(out)
}

func connectResult(out string) error {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "connected to"):
		return nil
	case out == "":
		return errors.New("adb did not answer")
	default:
		return errors.New(cleanMessage(strings.TrimPrefix(out, "failed to connect to ")))
	}
}

// Disconnect disconnects a TCP/IP device.
func (c *Client) Disconnect(ctx context.Context, addr string) error {
	_, err := c.Run(ctx, "disconnect", addr)
	return err
}

// Pair pairs with a device in Wireless debugging, at its pairing address
// and with its six-digit code (Android 11 and later).
func (c *Client) Pair(ctx context.Context, addr, code string) error {
	out, err := c.Run(ctx, "pair", addr, code)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(out), "successfully paired") {
		return errors.New(cleanMessage(out))
	}
	return nil
}

// TCPIP restarts adbd on a USB device listening on port, so that it can
// be connected to over Wi-Fi.
func (c *Client) TCPIP(ctx context.Context, serial string, port int) error {
	_, err := c.Run(ctx, "-s", serial, "tcpip", strconv.Itoa(port))
	return err
}

var routeSrcRe = regexp.MustCompile(`\bdev\s+(\S+).*\bsrc\s+(\d+\.\d+\.\d+\.\d+)`)
var inetRe = regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+)/`)

// WiFiAddress returns the device's address on its Wi-Fi network.
func (c *Client) WiFiAddress(ctx context.Context, serial string) (string, error) {
	if out, err := c.Shell(ctx, serial, "ip", "route"); err == nil {
		if ip := ParseRouteAddress(out); ip != "" {
			return ip, nil
		}
	}
	for _, iface := range []string{"wlan0", "wlan1"} {
		out, err := c.Shell(ctx, serial, "ip", "-f", "inet", "addr", "show", iface)
		if err != nil {
			continue
		}
		if m := inetRe.FindStringSubmatch(out); m != nil {
			return m[1], nil
		}
	}
	return "", errors.New("the device is not on a Wi-Fi network")
}

// ParseRouteAddress finds the Wi-Fi source address in `ip route` output.
func ParseRouteAddress(out string) string {
	var fallback string
	for _, line := range strings.Split(out, "\n") {
		m := routeSrcRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if strings.HasPrefix(m[1], "wlan") {
			return m[2]
		}
		if fallback == "" && !strings.HasPrefix(m[1], "rmnet") && !strings.HasPrefix(m[1], "dummy") {
			fallback = m[2]
		}
	}
	return fallback
}

// Screencap returns a PNG of the device's screen.
func (c *Client) Screencap(ctx context.Context, serial string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := c.Command(ctx, "-s", serial, "exec-out", "screencap", "-p")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(cleanMessage(msg))
	}
	data := stdout.Bytes()
	if !bytes.HasPrefix(data, []byte("\x89PNG")) {
		return nil, fmt.Errorf("the device returned no image")
	}
	return data, nil
}

// Push copies a file to the device.
func (c *Client) Push(ctx context.Context, serial, local, remote string) error {
	_, err := c.Run(ctx, "-s", serial, "push", local, remote)
	return err
}

// Reverse has the device's remote socket connect to the computer's local
// one, as "localabstract:name" to "tcp:port".
func (c *Client) Reverse(ctx context.Context, serial, remote, local string) error {
	_, err := c.Run(ctx, "-s", serial, "reverse", remote, local)
	return err
}

// ReverseRemove undoes Reverse.
func (c *Client) ReverseRemove(ctx context.Context, serial, remote string) error {
	_, err := c.Run(ctx, "-s", serial, "reverse", "--remove", remote)
	return err
}

// Forward has the computer's local socket connect to the device's remote
// one, as "tcp:port" to "localabstract:name".
func (c *Client) Forward(ctx context.Context, serial, local, remote string) error {
	_, err := c.Run(ctx, "-s", serial, "forward", local, remote)
	return err
}

// ForwardRemove undoes Forward.
func (c *Client) ForwardRemove(ctx context.Context, serial, local string) error {
	_, err := c.Run(ctx, "-s", serial, "forward", "--remove", local)
	return err
}

// KeyEvent sends an Android key event through the shell, for devices that
// are not mirrored.
func (c *Client) KeyEvent(ctx context.Context, serial string, keycode int) error {
	_, err := c.Shell(ctx, serial, "input", "keyevent", strconv.Itoa(keycode))
	return err
}

// MDNSService is a device that announces itself for Wireless debugging.
type MDNSService struct {
	Name    string // instance name, e.g. adb-R5CT1234-AbCdEf
	Type    string // _adb-tls-pairing._tcp or _adb-tls-connect._tcp
	Address string // host:port
}

// Pairing reports whether the service waits to be paired.
func (s MDNSService) Pairing() bool { return strings.HasPrefix(s.Type, "_adb-tls-pairing") }

// MDNSServices lists the services adb discovered on the network.
func (c *Client) MDNSServices(ctx context.Context) ([]MDNSService, error) {
	out, err := c.Run(ctx, "mdns", "services")
	if err != nil {
		return nil, err
	}
	return ParseMDNSServices(out), nil
}

// ParseMDNSServices reads the output of `adb mdns services`.
func ParseMDNSServices(out string) []MDNSService {
	var list []MDNSService
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || !strings.HasPrefix(f[1], "_adb") {
			continue
		}
		addr := f[len(f)-1]
		if !strings.Contains(addr, ":") {
			continue
		}
		list = append(list, MDNSService{Name: f[0], Type: strings.TrimSuffix(f[1], "."), Address: addr})
	}
	return list
}

// IsWiFiSerial reports whether a serial names a device attached over the
// network rather than USB.
func IsWiFiSerial(serial string) bool {
	if strings.Contains(serial, "._adb-tls-connect.") || strings.Contains(serial, "._tcp") {
		return true
	}
	host, port, ok := strings.Cut(serial, ":")
	if !ok || host == "" {
		return false
	}
	_, err := strconv.Atoi(port)
	return err == nil
}

// waitFor polls cond until it holds or ctx ends.
func waitFor(ctx context.Context, every time.Duration, cond func() bool) bool {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if cond() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
		}
	}
}
