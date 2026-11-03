package adb

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Info describes a device for people: its names and Android version.
type Info struct {
	Serial       string // adb's serial, which the transport changes
	HWSerial     string // ro.serialno, the same over USB and Wi-Fi
	Manufacturer string // "OnePlus"
	Brand        string
	Model        string // "PJF110"
	MarketName   string // "OnePlus Ace 3V"
	Release      string // "15"
	SDK          string // "35"
}

// Name is the device's name as people know it.
func (i Info) Name() string {
	if i.MarketName != "" {
		return i.MarketName
	}
	maker := i.Maker()
	if maker != "" && i.Model != "" && !strings.HasPrefix(strings.ToLower(i.Model), strings.ToLower(maker)) {
		return maker + " " + i.Model
	}
	if i.Model != "" {
		return i.Model
	}
	return i.Serial
}

// Maker is the manufacturer or brand, capitalized as the maker writes it.
func (i Info) Maker() string {
	m := i.Manufacturer
	if m == "" {
		m = i.Brand
	}
	if m == "" {
		return ""
	}
	if strings.ToLower(m) == m { // "samsung", "google"
		return strings.ToUpper(m[:1]) + m[1:]
	}
	return m
}

// Subtitle is "OnePlus · PJF110 · Android 15".
func (i Info) Subtitle() string {
	var parts []string
	if m := i.Maker(); m != "" {
		parts = append(parts, m)
	}
	if i.Model != "" {
		parts = append(parts, i.Model)
	}
	if i.Release != "" {
		parts = append(parts, "Android "+i.Release)
	}
	return strings.Join(parts, " · ")
}

// Identity keys what AirDroid remembers about the device: the hardware serial,
// else adb's.
func (i Info) Identity() string {
	if i.HWSerial != "" {
		return i.HWSerial
	}
	return i.Serial
}

// marketNameProps are where makers keep the name they sell a phone by.
var marketNameProps = []string{
	"ro.product.marketname",
	"ro.product.vendor.marketname",
	"ro.product.odm.marketname",
	"ro.vendor.oplus.market.name",
	"ro.oppo.market.name",
	"ro.vivo.market.name",
	"ro.config.marketing_name",
	"ro.product.nickname",
	"ro.semc.product.name",
}

// InfoFromProps builds Info out of getprop's properties.
func InfoFromProps(serial string, p map[string]string) Info {
	i := Info{
		Serial:       serial,
		HWSerial:     first(p, "ro.serialno", "ro.boot.serialno"),
		Manufacturer: first(p, "ro.product.manufacturer", "ro.product.vendor.manufacturer"),
		Brand:        first(p, "ro.product.brand", "ro.product.vendor.brand"),
		Model:        first(p, "ro.product.model", "ro.product.vendor.model"),
		Release:      first(p, "ro.build.version.release_or_codename", "ro.build.version.release"),
		SDK:          p["ro.build.version.sdk"],
	}
	i.MarketName = first(p, marketNameProps...)
	if i.MarketName == i.Model {
		i.MarketName = ""
	}
	return i
}

func first(p map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(p[k]); v != "" {
			return v
		}
	}
	return ""
}

// Info reads a ready device's description.
func (c *Client) Info(ctx context.Context, serial string) (Info, error) {
	props, err := c.Props(ctx, serial)
	if err != nil {
		return Info{Serial: serial}, err
	}
	info := InfoFromProps(serial, props)
	if info.MarketName == "" {
		// The name the user gave the phone, as "Ada's Pixel", is better than
		// a model number.
		if out, err := c.Shell(ctx, serial, "settings", "get", "global", "device_name"); err == nil {
			out = strings.TrimSpace(out)
			if out != "" && out != "null" && !strings.EqualFold(out, info.Model) {
				info.MarketName = out
			}
		}
	}
	return info, nil
}

// SwitchToWiFi has a USB device listen for adb over Wi-Fi on port 5555 and
// connects to it there. It returns the address it connected to.
func (c *Client) SwitchToWiFi(ctx context.Context, serial string) (string, error) {
	ip, err := c.WiFiAddress(ctx, serial)
	if err != nil {
		return "", err
	}
	if err := c.TCPIP(ctx, serial, 5555); err != nil {
		return "", err
	}
	addr := ip + ":5555"
	var last error
	ok := waitFor(ctx, 500*time.Millisecond, func() bool {
		last = c.Connect(ctx, addr)
		return last == nil
	})
	if !ok {
		if last == nil {
			last = ctx.Err()
		}
		return "", fmt.Errorf("could not connect to %s: %w", addr, last)
	}
	return addr, nil
}

// QRPairing is a pairing offer shown as a QR code, which Android's "Pair
// device with QR code" scans, as Android Studio does.
type QRPairing struct {
	Name     string
	Password string
}

// NewQRPairing makes a pairing offer with a random name and password.
func NewQRPairing() QRPairing {
	return QRPairing{Name: "airdroid-" + randomString(6), Password: randomString(10)}
}

// Payload is the text of the QR code.
func (q QRPairing) Payload() string {
	return fmt.Sprintf("WIFI:T:ADB;S:%s;P:%s;;", q.Name, q.Password)
}

// Wait waits for the phone that scanned the code, pairs with it and
// connects to it. It returns the address it connected to.
func (c *Client) PairQR(ctx context.Context, q QRPairing, progress func(string)) (string, error) {
	var svc MDNSService
	progress("Waiting for the phone to scan the code…")
	found := waitFor(ctx, time.Second, func() bool {
		list, err := c.MDNSServices(ctx)
		if err != nil {
			return false
		}
		for _, s := range list {
			if s.Pairing() && s.Name == q.Name {
				svc = s
				return true
			}
		}
		return false
	})
	if !found {
		return "", ctx.Err()
	}
	progress("Pairing…")
	if err := c.Pair(ctx, svc.Address, q.Password); err != nil {
		return "", err
	}
	progress("Connecting…")
	return c.connectAfterPairing(ctx, hostOf(svc.Address))
}

// PairCode pairs with the code Android shows under "Pair device with
// pairing code", then connects to the phone. connectAddr may be empty: the
// phone's connect service is then found over mDNS.
func (c *Client) PairCode(ctx context.Context, pairAddr, code, connectAddr string, progress func(string)) (string, error) {
	progress("Pairing…")
	if err := c.Pair(ctx, pairAddr, code); err != nil {
		return "", err
	}
	progress("Connecting…")
	if connectAddr != "" {
		if err := c.Connect(ctx, connectAddr); err != nil {
			return "", err
		}
		return connectAddr, nil
	}
	return c.connectAfterPairing(ctx, hostOf(pairAddr))
}

// connectAfterPairing finds the phone's connect service at host and
// connects to it. adb often connects by itself as the service appears.
func (c *Client) connectAfterPairing(ctx context.Context, host string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var addr string
	var last error
	ok := waitFor(ctx, time.Second, func() bool {
		list, err := c.MDNSServices(ctx)
		if err != nil {
			last = err
			return false
		}
		for _, s := range list {
			if !s.Pairing() && hostOf(s.Address) == host {
				if last = c.Connect(ctx, s.Address); last == nil {
					addr = s.Address
					return true
				}
			}
		}
		return false
	})
	if !ok {
		if last == nil {
			last = fmt.Errorf("paired, but the phone did not show up on the network: connect to the address under Wireless debugging")
		}
		return "", last
	}
	return addr, nil
}

func hostOf(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}

const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"

func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			b[i] = alphabet[i%len(alphabet)]
			continue
		}
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}
